use crate::error::{err_bad, redact, IntuitionError};
use crate::policy::{
    as_f64, bind_target, classify_boolean, classify_interrupt, decide_disposition, resolve_policy,
    score_band, Bound, Policy, ScoreRead, TargetCandidate, Tri,
};
use crate::roles::{
    opening_kind_for, opening_question, prior_for_role, tactics_for_role, tension_levels,
    threat_levels, world_directives, HOLD_DIRECTIVE, HOLD_TACTIC,
};
use serde::Serialize;
use serde_json::{Map, Value};
use std::collections::HashSet;
use std::sync::{Arc, Mutex};
use std::time::Instant;

pub type Judge<'a> =
    dyn Fn(&Value, &Value) -> Result<JudgeResult, IntuitionError> + Send + Sync + 'a;

#[derive(Debug, Clone)]
pub struct JudgeResult {
    pub answers: Map<String, Value>,
    pub provider_metadata: Map<String, Value>,
    pub model_id: String,
    pub usage: Option<TokenUsage>,
}

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct TokenUsage {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub input_tokens: Option<f64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub output_tokens: Option<f64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub total_tokens: Option<f64>,
}

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Impulse {
    pub schema_version: u8,
    pub agent_id: String,
    pub role: String,
    pub disposition: String,
    pub tactic: String,
    pub suggested_tactic: String,
    pub target_id: Option<String>,
    pub suggested_target_id: Option<String>,
    pub target_source: String,
    pub because: String,
    pub interrupt: Tri,
    pub opening: Tri,
    pub opening_kind: String,
    pub player_hostile: Tri,
    pub ally_needs_help: Tri,
    pub threat: ScoreRead,
    pub confidence: Option<f64>,
    pub confidence_source: String,
    pub probabilities: Option<Map<String, Value>>,
    pub model_id: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub usage: Option<TokenUsage>,
    pub latency_ms: i64,
}

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct WorldImpulse {
    pub schema_version: u8,
    pub disposition: String,
    pub directive: String,
    pub suggested_directive: String,
    pub because: String,
    pub tension: ScoreRead,
    pub player_overextended: Tri,
    pub confidence: Option<f64>,
    pub confidence_source: String,
    pub probabilities: Option<Map<String, Value>>,
    pub model_id: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub usage: Option<TokenUsage>,
    pub latency_ms: i64,
}

#[derive(Clone)]
struct Vec3 {
    x: f64,
    y: f64,
    z: Option<f64>,
}

#[derive(Clone)]
struct Entity {
    id: String,
    kind: String,
    name: String,
    faction: String,
    relation: String,
    activity: String,
    distance: Option<f64>,
    health: Option<f64>,
    visible: Option<bool>,
    tags: Vec<String>,
    pos: Option<Vec3>,
}

#[derive(Clone)]
struct Scene {
    place: String,
    time_of_day: String,
    weather: String,
    current_directive: String,
    seconds_on_directive: Option<f64>,
    recent_events: Vec<String>,
}

#[derive(Clone)]
struct Agent {
    id: String,
    role: String,
    name: String,
    personality: String,
    goal: String,
    faction: String,
    activity: String,
    current_tactic: String,
    health: Option<f64>,
    stamina: Option<f64>,
    seconds_on_tactic: Option<f64>,
    current_target_id: Option<String>,
    memory: Vec<String>,
    pos: Option<Vec3>,
}

#[derive(Clone)]
struct Presence {
    id: String,
    role: String,
    activity: String,
    faction: String,
    health: Option<f64>,
}

#[derive(Clone)]
struct WorldPlayer {
    activity: String,
    dominance: String,
    health: Option<f64>,
}

#[derive(Clone)]
struct SenseReq {
    scene: Scene,
    agent: Agent,
    nearby: Vec<Entity>,
    player: Option<Entity>,
    tactics: Vec<(String, String)>,
    policy: Policy,
}

#[derive(Clone)]
struct WorldReq {
    scene: Scene,
    presences: Vec<Presence>,
    player: Option<WorldPlayer>,
    directives: Vec<(String, String)>,
    policy: Policy,
}

struct RosterEntry {
    id: String,
    choice_key: String,
    kind: String,
    name: String,
    faction: String,
    relation: String,
    distance: Option<f64>,
    visible: Option<bool>,
    health: Option<f64>,
    activity: String,
    tags: Vec<String>,
}

pub fn sense_agent(input: &Value, judge: &Judge<'_>) -> Result<Impulse, IntuitionError> {
    sense_agent_req(validate_sense(input)?, judge)
}

fn sense_agent_req(req: SenseReq, judge: &Judge<'_>) -> Result<Impulse, IntuitionError> {
    let started = Instant::now();
    if req.agent.health.is_some_and(|h| h <= 0.0) {
        return Ok(incapacitated(&req, started));
    }
    let (state, roster, has_player) = compact_sense(&req);
    let ask_ally = roster.iter().any(|e| e.relation == "ally");
    let questions =
        build_agent_questions(&req.tactics, &roster, &req.agent.role, has_player, ask_ally);
    let result = judge(&state, &questions)?;
    compose_agent(&req, &roster, has_player, ask_ally, result, started)
}

pub fn sense_world(input: &Value, judge: &Judge<'_>) -> Result<WorldImpulse, IntuitionError> {
    sense_world_req(validate_world(input)?, judge)
}

fn sense_world_req(req: WorldReq, judge: &Judge<'_>) -> Result<WorldImpulse, IntuitionError> {
    let started = Instant::now();
    let result = judge(
        &compact_world(&req),
        &build_world_questions(&req.directives, req.player.is_some()),
    )?;
    compose_world(&req, result, started)
}

pub fn sense_tick(input: &Value, judge: &Judge<'_>) -> Result<Value, IntuitionError> {
    let started = Instant::now();
    let req = validate_tick(input)?;
    let world_out = Arc::new(Mutex::new(Value::Null));
    let agents_out = Arc::new(Mutex::new(vec![Value::Null; req.agents.len()]));
    let concurrency = req.concurrency.max(1);
    let (tx, rx) = std::sync::mpsc::sync_channel(concurrency);
    for _ in 0..concurrency {
        let _ = tx.send(());
    }
    let rx = Arc::new(Mutex::new(rx));
    let tx = Arc::new(tx);
    std::thread::scope(|scope| {
        if let Some(world) = req.world.clone() {
            let world_out = Arc::clone(&world_out);
            let rx = Arc::clone(&rx);
            let tx = Arc::clone(&tx);
            scope.spawn(move || {
                let _permit = rx.lock().unwrap().recv();
                let value = match sense_world_req(world, judge) {
                    Ok(v) => serde_json::to_value(v).unwrap_or(Value::Null),
                    Err(err) => serde_json::json!({"error": err.message, "status": err.status}),
                };
                *world_out.lock().unwrap() = value;
                let _ = tx.send(());
            });
        }
        for (index, agent) in req.agents.iter().cloned().enumerate() {
            let agents_out = Arc::clone(&agents_out);
            let rx = Arc::clone(&rx);
            let tx = Arc::clone(&tx);
            let agent_id = agent.agent.id.clone();
            scope.spawn(move || {
                let _permit = rx.lock().unwrap().recv();
                let value = match sense_agent_req(agent, judge) {
                    Ok(v) => serde_json::to_value(v).unwrap_or(Value::Null),
                    Err(err) => serde_json::json!({"agentId": agent_id, "error": err.message, "status": err.status}),
                };
                agents_out.lock().unwrap()[index] = value;
                let _ = tx.send(());
            });
        }
    });
    Ok(serde_json::json!({
        "schemaVersion": 1,
        "scene": {"place": req.scene.place},
        "world": world_out.lock().unwrap().clone(),
        "agents": agents_out.lock().unwrap().clone(),
        "latencyMs": started.elapsed().as_millis() as i64,
    }))
}

fn compose_agent(
    req: &SenseReq,
    roster: &[RosterEntry],
    has_player: bool,
    ask_ally: bool,
    result: JudgeResult,
    started: Instant,
) -> Result<Impulse, IntuitionError> {
    let tactic = require_choice(&result.answers, "tactic")?;
    if !req.tactics.iter().any(|(k, _)| k == &tactic.choice) {
        return Err(err_bad(
            502,
            format!("Jev 返回了未声明的战术 {}", tactic.choice),
        ));
    }
    let threat = require_score(&result.answers, "threat")?;
    let interrupt = classify_interrupt(
        read_probability(&result.answers, "interrupt"),
        req.policy.interrupt_at,
    );
    let (confidence, source) = resolve_confidence(
        tactic.confidence,
        &result.provider_metadata,
        "tactic",
        tactic.probabilities.as_ref(),
    );
    let hold_key = req
        .tactics
        .iter()
        .any(|(k, _)| k == HOLD_TACTIC)
        .then_some(HOLD_TACTIC);
    let decision = decide_disposition(
        &req.agent.current_tactic,
        &tactic.choice,
        confidence,
        interrupt,
        hold_key,
        req.policy.switch_confidence,
    );
    let model_target = model_target_id(&result.answers, roster);
    let candidates = candidates_of(roster);
    let mut bound = bind_target(&decision.executing, &model_target, &candidates);
    if let Some(kept) = keep_current_target(
        &decision.disposition,
        &decision.executing,
        req.agent.current_target_id.as_deref(),
        roster,
    ) {
        bound = kept;
    }
    let suggested_name =
        if hold_key == Some(tactic.choice.as_str()) && !req.agent.current_tactic.is_empty() {
            req.agent.current_tactic.clone()
        } else {
            tactic.choice.clone()
        };
    let suggested = bind_target(&suggested_name, &model_target, &candidates);
    Ok(Impulse {
        schema_version: 1,
        agent_id: req.agent.id.clone(),
        role: req.agent.role.clone(),
        disposition: decision.disposition,
        tactic: decision.executing,
        suggested_tactic: tactic.choice,
        target_id: bound.target_id,
        suggested_target_id: suggested.target_id,
        target_source: bound.target_source,
        because: decision.because,
        interrupt,
        opening: classify_boolean(
            read_probability(&result.answers, "opening"),
            req.policy.uncertain_margin,
        ),
        opening_kind: opening_kind_for(&req.agent.role).into(),
        player_hostile: if has_player {
            classify_boolean(
                read_probability(&result.answers, "playerHostile"),
                req.policy.uncertain_margin,
            )
        } else {
            Tri::False
        },
        ally_needs_help: if ask_ally {
            classify_boolean(
                read_probability(&result.answers, "allyNeedsHelp"),
                req.policy.uncertain_margin,
            )
        } else {
            Tri::False
        },
        threat: score_band(threat, &threat_levels()),
        confidence,
        confidence_source: source,
        probabilities: tactic.probabilities,
        model_id: if result.model_id.is_empty() {
            crate::judge::model_id()
        } else {
            result.model_id
        },
        usage: result.usage,
        latency_ms: started.elapsed().as_millis() as i64,
    })
}

fn compose_world(
    req: &WorldReq,
    result: JudgeResult,
    started: Instant,
) -> Result<WorldImpulse, IntuitionError> {
    let directive = require_choice(&result.answers, "directive")?;
    if !req.directives.iter().any(|(k, _)| k == &directive.choice) {
        return Err(err_bad(
            502,
            format!("Jev 返回了未声明的拍子 {}", directive.choice),
        ));
    }
    let tension = require_score(&result.answers, "tension")?;
    let (confidence, source) = resolve_confidence(
        directive.confidence,
        &result.provider_metadata,
        "directive",
        directive.probabilities.as_ref(),
    );
    let hold_key = req
        .directives
        .iter()
        .any(|(k, _)| k == HOLD_DIRECTIVE)
        .then_some(HOLD_DIRECTIVE);
    let decision = decide_disposition(
        &req.scene.current_directive,
        &directive.choice,
        confidence,
        Tri::False,
        hold_key,
        req.policy.switch_confidence,
    );
    Ok(WorldImpulse {
        schema_version: 1,
        disposition: decision.disposition,
        directive: decision.executing,
        suggested_directive: directive.choice,
        because: decision.because,
        tension: score_band(tension, &tension_levels()),
        player_overextended: if req.player.is_some() {
            classify_boolean(
                read_probability(&result.answers, "overextended"),
                req.policy.uncertain_margin,
            )
        } else {
            Tri::False
        },
        confidence,
        confidence_source: source,
        probabilities: directive.probabilities,
        model_id: if result.model_id.is_empty() {
            crate::judge::model_id()
        } else {
            result.model_id
        },
        usage: result.usage,
        latency_ms: started.elapsed().as_millis() as i64,
    })
}

fn incapacitated(req: &SenseReq, started: Instant) -> Impulse {
    Impulse {
        schema_version: 1,
        agent_id: req.agent.id.clone(),
        role: req.agent.role.clone(),
        disposition: "incapacitated".into(),
        tactic: "none".into(),
        suggested_tactic: "none".into(),
        target_id: None,
        suggested_target_id: None,
        target_source: "none".into(),
        because: "incapacitated".into(),
        interrupt: Tri::False,
        opening: Tri::False,
        opening_kind: opening_kind_for(&req.agent.role).into(),
        player_hostile: Tri::False,
        ally_needs_help: Tri::False,
        threat: score_band(3.0, &threat_levels()),
        confidence: None,
        confidence_source: "none".into(),
        probabilities: None,
        model_id: "skipped".into(),
        usage: None,
        latency_ms: started.elapsed().as_millis() as i64,
    }
}

struct ChoiceAnswer {
    choice: String,
    probabilities: Option<Map<String, Value>>,
    confidence: Option<f64>,
}

fn require_choice(answers: &Map<String, Value>, id: &str) -> Result<ChoiceAnswer, IntuitionError> {
    let choice = answers
        .get(id)
        .and_then(|v| v.get("choice"))
        .and_then(|v| v.as_str())
        .unwrap_or("")
        .to_string();
    if choice.is_empty() {
        let keys = answers.keys().cloned().collect::<Vec<_>>().join(", ");
        return Err(err_bad(502, format!("Jev 没有返回 {id}。答案键：{keys}")));
    }
    let raw = answers.get(id).and_then(|v| v.as_object());
    Ok(ChoiceAnswer {
        choice,
        probabilities: raw.and_then(|r| read_distribution(r.get("probabilities"))),
        confidence: raw.and_then(|r| r.get("confidence")).and_then(read_unit),
    })
}

fn require_score(answers: &Map<String, Value>, id: &str) -> Result<f64, IntuitionError> {
    let score = answers
        .get(id)
        .and_then(|v| v.get("score"))
        .and_then(as_f64)
        .filter(|n| n.is_finite());
    score.ok_or_else(|| err_bad(502, format!("Jev 没有返回 {id}")))
}

fn read_probability(answers: &Map<String, Value>, id: &str) -> Option<f64> {
    let raw = answers.get(id)?.as_object()?;
    raw.get("probability")
        .and_then(read_unit)
        .or_else(|| raw.get("noul").and_then(read_unit))
}

fn read_unit(v: &Value) -> Option<f64> {
    as_f64(v).filter(|n| n.is_finite() && (0.0..=1.0).contains(n))
}

fn read_distribution(v: Option<&Value>) -> Option<Map<String, Value>> {
    let raw = v?.as_object()?;
    let mut out = Map::new();
    for (key, item) in raw {
        if let Some(n) = as_f64(item).filter(|n| n.is_finite()) {
            out.insert(key.clone(), serde_json::json!(n));
        }
    }
    (!out.is_empty()).then_some(out)
}

fn resolve_confidence(
    answer: Option<f64>,
    metadata: &Map<String, Value>,
    id: &str,
    probabilities: Option<&Map<String, Value>>,
) -> (Option<f64>, String) {
    if answer.is_some() {
        return (answer, "answer".into());
    }
    if let Some(meta) = read_meta_confidence(metadata, id) {
        return (Some(meta), "typesafe".into());
    }
    if let Some(margin) = confidence_from_distribution(probabilities) {
        return (Some(margin), "margin".into());
    }
    (None, "none".into())
}

fn read_meta_confidence(metadata: &Map<String, Value>, id: &str) -> Option<f64> {
    let mut buckets = Vec::new();
    if let Some(v) = metadata.get("typesafe") {
        buckets.push(v);
    }
    if let Some(v) = metadata.get("typesafe-ai") {
        buckets.push(v);
    }
    if let Some(v) = metadata.get("gateway").and_then(|g| g.get("typesafe")) {
        buckets.push(v);
    }
    for bucket in buckets {
        let conf = bucket.get("confidence")?.as_object()?;
        if let Some(value) = conf.get(id).and_then(read_unit) {
            return Some(value);
        }
    }
    None
}

fn confidence_from_distribution(probabilities: Option<&Map<String, Value>>) -> Option<f64> {
    let mut values: Vec<f64> = probabilities?.values().filter_map(as_f64).collect();
    if values.is_empty() {
        return None;
    }
    values.sort_by(|a, b| b.total_cmp(a));
    Some(((values[0] - values.get(1).copied().unwrap_or(0.0)) * 100.0).round() / 100.0)
}

fn model_target_id(answers: &Map<String, Value>, roster: &[RosterEntry]) -> String {
    let Ok(target) = require_choice(answers, "target") else {
        return "none".into();
    };
    if target.choice == "none" {
        return "none".into();
    }
    roster
        .iter()
        .find(|e| e.choice_key == target.choice)
        .map(|e| e.id.clone())
        .unwrap_or_else(|| "none".into())
}

fn candidates_of(roster: &[RosterEntry]) -> Vec<TargetCandidate> {
    roster
        .iter()
        .map(|e| TargetCandidate {
            id: e.id.clone(),
            relation: e.relation.clone(),
            distance: e.distance,
            health: e.health,
            kind: e.kind.clone(),
        })
        .collect()
}

fn keep_current_target(
    disposition: &str,
    tactic: &str,
    current: Option<&str>,
    roster: &[RosterEntry],
) -> Option<Bound> {
    if disposition != "continue" && disposition != "hold" {
        return None;
    }
    let current = current.filter(|id| !id.is_empty())?;
    if !crate::policy::tactic_needs_target(tactic) {
        return None;
    }
    roster.iter().find(|e| e.id == current).map(|_| Bound {
        target_id: Some(current.into()),
        target_source: "kept".into(),
    })
}

fn compact_sense(req: &SenseReq) -> (Value, Vec<RosterEntry>, bool) {
    let mut agent = req.agent.clone();
    if let Some((personality, goal)) = prior_for_role(&agent.role) {
        if agent.personality.is_empty() {
            agent.personality = personality.into();
        }
        if agent.goal.is_empty() {
            agent.goal = goal.into();
        }
    }
    let roster = build_roster(&agent, req.player.as_ref(), &req.nearby);
    let mut state = Map::new();
    let scene = omit_scene(&req.scene, false);
    if !scene.is_empty() {
        state.insert("scene".into(), Value::Object(scene));
    }
    state.insert("agent".into(), Value::Object(omit_agent(&agent)));
    let mut has_player = false;
    let mut nearby = Vec::new();
    for entry in &roster {
        if req.player.as_ref().is_some_and(|p| p.id == entry.id) {
            state.insert("player".into(), public_entry(entry));
            has_player = true;
        } else {
            nearby.push(public_entry(entry));
        }
    }
    if !nearby.is_empty() {
        state.insert("nearby".into(), Value::Array(nearby));
    }
    (Value::Object(state), roster, has_player)
}

fn compact_world(req: &WorldReq) -> Value {
    let mut state = Map::new();
    let scene = omit_scene(&req.scene, true);
    if !scene.is_empty() {
        state.insert("scene".into(), Value::Object(scene));
    }
    if let Some(player) = &req.player {
        let mut row = Map::new();
        put_str(&mut row, "activity", &player.activity);
        if let Some(h) = player.health {
            row.insert("health".into(), serde_json::json!(h));
        }
        put_str(&mut row, "dominance", &player.dominance);
        if !row.is_empty() {
            state.insert("player".into(), Value::Object(row));
        }
    }
    if !req.presences.is_empty() {
        state.insert(
            "presences".into(),
            Value::Array(
                req.presences
                    .iter()
                    .map(|item| {
                        let mut row = Map::new();
                        row.insert("id".into(), Value::String(item.id.clone()));
                        row.insert("role".into(), Value::String(item.role.clone()));
                        put_str(&mut row, "activity", &item.activity);
                        put_str(&mut row, "faction", &item.faction);
                        if let Some(h) = item.health {
                            row.insert("health".into(), serde_json::json!(h));
                        }
                        Value::Object(row)
                    })
                    .collect(),
            ),
        );
    }
    Value::Object(state)
}

fn omit_scene(s: &Scene, world: bool) -> Map<String, Value> {
    let mut out = Map::new();
    put_str(&mut out, "place", &s.place);
    put_str(&mut out, "timeOfDay", &s.time_of_day);
    put_str(&mut out, "weather", &s.weather);
    if world {
        put_str(&mut out, "currentDirective", &s.current_directive);
        if let Some(n) = s.seconds_on_directive {
            out.insert("secondsOnDirective".into(), serde_json::json!(n));
        }
    }
    if !s.recent_events.is_empty() {
        out.insert(
            "recentEvents".into(),
            Value::Array(s.recent_events.iter().cloned().map(Value::String).collect()),
        );
    }
    out
}

fn omit_agent(a: &Agent) -> Map<String, Value> {
    let mut out = Map::new();
    out.insert("id".into(), Value::String(a.id.clone()));
    out.insert("role".into(), Value::String(a.role.clone()));
    put_str(&mut out, "name", &a.name);
    put_str(&mut out, "personality", &a.personality);
    put_str(&mut out, "goal", &a.goal);
    put_str(&mut out, "faction", &a.faction);
    put_str(&mut out, "activity", &a.activity);
    put_str(&mut out, "currentTactic", &a.current_tactic);
    if let Some(n) = a.health {
        out.insert("health".into(), serde_json::json!(n));
    }
    if let Some(n) = a.stamina {
        out.insert("stamina".into(), serde_json::json!(n));
    }
    if let Some(id) = a.current_target_id.as_ref().filter(|s| !s.is_empty()) {
        out.insert("currentTargetId".into(), Value::String(id.clone()));
    }
    if let Some(n) = a.seconds_on_tactic {
        out.insert("secondsOnTactic".into(), serde_json::json!(n));
    }
    if !a.memory.is_empty() {
        out.insert(
            "memory".into(),
            Value::Array(a.memory.iter().cloned().map(Value::String).collect()),
        );
    }
    out
}

fn put_str(map: &mut Map<String, Value>, key: &str, value: &str) {
    if !value.is_empty() {
        map.insert(key.into(), Value::String(value.into()));
    }
}

fn build_roster(agent: &Agent, player: Option<&Entity>, nearby: &[Entity]) -> Vec<RosterEntry> {
    let mut rows = Vec::new();
    if let Some(player) = player.filter(|p| p.id != agent.id) {
        rows.push(player.clone());
    }
    let mut sorted: Vec<Entity> = nearby
        .iter()
        .filter(|e| e.id != agent.id && player.is_none_or(|p| p.id != e.id))
        .cloned()
        .collect();
    sorted.sort_by(|a, b| measured(agent, a).total_cmp(&measured(agent, b)));
    rows.extend(sorted.into_iter().take(12));
    rows.truncate(16);
    let mut used = HashSet::from(["none".into()]);
    rows.into_iter()
        .map(|item| to_entry(agent, &item, &mut used))
        .collect()
}

fn to_entry(agent: &Agent, item: &Entity, used: &mut HashSet<String>) -> RosterEntry {
    let distance = measured(agent, item);
    let dist = (distance < 1e8).then(|| (distance * 10.0).round() / 10.0);
    let mut activity = item.activity.clone();
    if activity.chars().count() > 120 {
        activity = activity.chars().take(120).collect();
    }
    RosterEntry {
        id: item.id.clone(),
        choice_key: choice_key(&item.id, used),
        kind: item.kind.clone(),
        name: if item.name.is_empty() {
            item.id.clone()
        } else {
            item.name.clone()
        },
        faction: item.faction.clone(),
        relation: relation_of(agent, item),
        distance: dist,
        visible: item.visible,
        health: item.health,
        activity,
        tags: item.tags.iter().take(4).cloned().collect(),
    }
}

fn relation_of(agent: &Agent, item: &Entity) -> String {
    if !item.relation.is_empty() {
        return item.relation.clone();
    }
    if !agent.faction.is_empty() && agent.faction == item.faction {
        return "ally".into();
    }
    "unknown".into()
}

fn measured(agent: &Agent, item: &Entity) -> f64 {
    if let Some(d) = item.distance {
        return d;
    }
    match (&agent.pos, &item.pos) {
        (Some(a), Some(b)) => {
            let az = a.z.unwrap_or(0.0);
            let bz = b.z.unwrap_or(0.0);
            (a.x - b.x).hypot((a.y - b.y).hypot(az - bz))
        }
        _ => 1e9,
    }
}

fn choice_key(id: &str, used: &mut HashSet<String>) -> String {
    let raw: String = id
        .chars()
        .map(|c| {
            if c.is_ascii_alphanumeric() || c == '_' {
                c
            } else {
                '_'
            }
        })
        .collect();
    let mut base: String = raw
        .trim_start_matches(|c: char| !c.is_ascii_alphabetic())
        .chars()
        .take(40)
        .collect();
    if base.is_empty() || base == "none" {
        base = "entity".into();
    }
    let mut key = base.clone();
    let mut n = 2;
    while used.contains(&key) {
        let prefix: String = base.chars().take(36).collect();
        key = format!("{prefix}_{n}");
        n += 1;
    }
    used.insert(key.clone());
    key
}

fn public_entry(entry: &RosterEntry) -> Value {
    let mut out = Map::new();
    out.insert("id".into(), Value::String(entry.choice_key.clone()));
    if !entry.name.is_empty() && entry.name != entry.choice_key {
        out.insert("name".into(), Value::String(entry.name.clone()));
    }
    out.insert("kind".into(), Value::String(entry.kind.clone()));
    put_str(&mut out, "faction", &entry.faction);
    out.insert("relation".into(), Value::String(entry.relation.clone()));
    if let Some(n) = entry.distance {
        out.insert("distance".into(), serde_json::json!(n));
    }
    if let Some(v) = entry.visible {
        out.insert("visible".into(), Value::Bool(v));
    }
    if let Some(n) = entry.health {
        out.insert("health".into(), serde_json::json!(n));
    }
    put_str(&mut out, "activity", &entry.activity);
    if !entry.tags.is_empty() {
        out.insert(
            "tags".into(),
            Value::Array(entry.tags.iter().cloned().map(Value::String).collect()),
        );
    }
    Value::Object(out)
}

fn build_agent_questions(
    tactics: &[(String, String)],
    roster: &[RosterEntry],
    role: &str,
    ask_player: bool,
    ask_ally: bool,
) -> Value {
    let (instructions, criteria) = opening_question(role);
    let mut questions = Map::new();
    questions.insert("tactic".into(), serde_json::json!({
        "type": "choice",
        "instructions": "Which single tactic should this agent commit to now? Use `agent.role`, `agent.personality`, `agent.goal`, `agent.health`, `agent.currentTactic`, `agent.activity`, `scene`, `player`, and `nearby`. Choose hold when the current tactic still matches the moment. Do not invent a tactic.",
        "criteria": pairs_value(tactics),
    }));
    questions.insert("threat".into(), serde_json::json!({
        "type": "score",
        "instructions": "How much danger is this agent in right now? Match `agent.health`, `player`, and `nearby` to a situation. Score this agent, not the whole scene.",
        "criteria": threat_levels(),
    }));
    questions.insert("interrupt".into(), serde_json::json!({
        "type": "boolean",
        "instructions": "Should this agent abort `agent.currentTactic` immediately? Use `agent.secondsOnTactic`, `agent.health`, `player`, and `nearby`.",
        "criteria": {"true": "The assumption behind the current tactic just broke: a new threat, a dying ally, or the target is gone.", "false": "The current tactic still fits, or there is no current tactic that needs aborting."},
    }));
    questions.insert("opening".into(), serde_json::json!({"type": "boolean", "instructions": instructions, "criteria": pairs_value(&criteria)}));
    if !roster.is_empty() {
        let mut criteria = Map::new();
        criteria.insert(
            "none".into(),
            Value::String(
                "No specific entity. The moment is about the place or the self, not a lock-on."
                    .into(),
            ),
        );
        for entry in roster {
            criteria.insert(
                entry.choice_key.clone(),
                Value::String(describe_entry(entry)),
            );
        }
        questions.insert("target".into(), serde_json::json!({
            "type": "choice",
            "instructions": "Which entity is the focus of this moment? Choose none when the agent should not lock onto anyone. The option id matches `id` on `player` or `nearby`.",
            "criteria": criteria,
        }));
    }
    if ask_player {
        questions.insert("playerHostile".into(), serde_json::json!({
            "type": "boolean",
            "instructions": "Is `player` about to attack this agent or an ally, as opposed to passing by, talking, or leaving?",
            "criteria": {"true": "A weapon is out, they are sprinting in, they just struck, or they are clearly hunting.", "false": "Sheathed, idle, talking, leaving, or moving past without a threat."},
        }));
    }
    if ask_ally {
        questions.insert("allyNeedsHelp".into(), serde_json::json!({
            "type": "boolean",
            "instructions": "Does an ally in `nearby` need this agent's help within the next few seconds?",
            "criteria": {"true": "An ally is hurt, falling, cornered, or calling for help.", "false": "Allies are fine, or none of them need this agent."},
        }));
    }
    Value::Object(questions)
}

fn build_world_questions(directives: &[(String, String)], ask_player: bool) -> Value {
    let mut questions = Map::new();
    questions.insert("directive".into(), serde_json::json!({
        "type": "choice",
        "instructions": "Which single beat should this place play now? Use `scene.currentDirective`, `scene.recentEvents`, `player`, and `presences`. Choose hold_atmosphere when the current beat still fits. Do not invent a beat.",
        "criteria": pairs_value(directives),
    }));
    questions.insert("tension".into(), serde_json::json!({
        "type": "score",
        "instructions": "Where does the place sit right now, as a situation rather than a vague intensity? Use `scene`, `player`, and `presences`.",
        "criteria": tension_levels(),
    }));
    if ask_player {
        questions.insert("overextended".into(), serde_json::json!({
            "type": "boolean",
            "instructions": "Is the player overextended: too deep, too hurt, or too committed, so the place could punish them?",
            "criteria": {"true": "The player is deep in, badly hurt, surrounded, or cut off from an easy step back.", "false": "The player has room, health, or an obvious way to step back."},
        }));
    }
    Value::Object(questions)
}

fn describe_entry(entry: &RosterEntry) -> String {
    let mut parts = vec![if !entry.name.is_empty() && entry.name != entry.id {
        format!("{} ({})", entry.name, entry.id)
    } else {
        entry.id.clone()
    }];
    parts.push(entry.kind.clone());
    parts.push(entry.relation.clone());
    parts.push(
        entry
            .distance
            .map(|n| format!("{n}m"))
            .unwrap_or_else(|| "distance unknown".into()),
    );
    parts.push(match entry.visible {
        Some(true) => "visible".into(),
        Some(false) => "not visible".into(),
        None => "visibility unknown".into(),
    });
    if let Some(h) = entry.health {
        parts.push(format!("health {h}"));
    }
    if !entry.activity.is_empty() {
        parts.push(entry.activity.clone());
    }
    if !entry.tags.is_empty() {
        parts.push(format!("tags {}", entry.tags.join(", ")));
    }
    parts.join(", ")
}

fn pairs_value(items: &[(String, String)]) -> Value {
    let mut map = Map::new();
    for (k, v) in items {
        map.insert(k.clone(), Value::String(v.clone()));
    }
    Value::Object(map)
}

fn validate_sense(input: &Value) -> Result<SenseReq, IntuitionError> {
    let raw = obj(input, "请求体")?;
    let scene = validate_scene(raw.get("scene").unwrap_or(&Value::Null))?;
    let agent = validate_agent(raw.get("agent").unwrap_or(&Value::Null))?;
    let nearby = validate_nearby(raw.get("nearby"))?;
    let player = match raw.get("player") {
        None | Some(Value::Null) => None,
        Some(v) => Some(validate_entity(v, "player", "player")?),
    };
    let tactics = if raw.get("tactics").is_none() || raw.get("tactics").is_some_and(|v| v.is_null())
    {
        tactics_for_role(&agent.role).ok_or_else(|| err_bad(400, format!("角色 \"{}\" 没有预置战术。请传 tactics。预置角色：guard、civilian、predator、companion、ambient", agent.role)))?
    } else {
        validate_option_map(raw.get("tactics").unwrap(), "tactics", 1, 24)?
    };
    let policy = policy_of(raw.get("policy"))?;
    Ok(SenseReq {
        scene,
        agent,
        nearby,
        player,
        tactics,
        policy,
    })
}

fn validate_world(input: &Value) -> Result<WorldReq, IntuitionError> {
    let raw = obj(input, "world")?;
    let scene = validate_scene(raw.get("scene").unwrap_or(&Value::Null))?;
    let presences = validate_presences(raw.get("presences"))?;
    let player = match raw.get("player") {
        None | Some(Value::Null) => None,
        Some(v) => {
            let obj = obj(v, "player")?;
            Some(WorldPlayer {
                activity: optional_text(obj.get("activity"), "player.activity", 160)?,
                dominance: optional_text(obj.get("dominance"), "player.dominance", 160)?,
                health: optional_unit(obj.get("health"), "player.health")?,
            })
        }
    };
    let directives =
        if raw.get("directives").is_none() || raw.get("directives").is_some_and(|v| v.is_null()) {
            world_directives()
        } else {
            validate_option_map(raw.get("directives").unwrap(), "directives", 1, 24)?
        };
    Ok(WorldReq {
        scene,
        presences,
        player,
        directives,
        policy: policy_of(raw.get("policy"))?,
    })
}

struct TickReq {
    scene: Scene,
    agents: Vec<SenseReq>,
    world: Option<WorldReq>,
    concurrency: usize,
}

fn validate_tick(input: &Value) -> Result<TickReq, IntuitionError> {
    let raw = obj(input, "请求体")?;
    let scene = validate_scene(raw.get("scene").unwrap_or(&Value::Null))?;
    let agents_raw = match raw.get("agents") {
        None | Some(Value::Null) => Vec::new(),
        Some(v) => v
            .as_array()
            .cloned()
            .ok_or_else(|| err_bad(400, "agents 必须是数组"))?,
    };
    if agents_raw.len() > 16 {
        return Err(err_bad(400, "单次 tick 最多 16 个 agent"));
    }
    let mut agents = Vec::new();
    for (i, item) in agents_raw.iter().enumerate() {
        let mut obj = obj(item, &format!("agents[{i}]"))?.clone();
        obj.insert("scene".into(), scene_value(&scene));
        agents.push(validate_sense(&Value::Object(obj))?);
    }
    let world = match raw.get("world") {
        None | Some(Value::Null) | Some(Value::Bool(false)) => None,
        Some(Value::Bool(true)) => Some(validate_world(
            &serde_json::json!({"scene": scene_value(&scene)}),
        )?),
        Some(v) => {
            let mut obj = obj(v, "world")?.clone();
            obj.insert("scene".into(), scene_value(&scene));
            Some(validate_world(&Value::Object(obj))?)
        }
    };
    if agents.is_empty() && world.is_none() {
        return Err(err_bad(
            400,
            "tick 至少要有一个 agent，或把 world 设为 true",
        ));
    }
    let concurrency = match raw.get("concurrency") {
        None | Some(Value::Null) => 4,
        Some(v) => {
            let n = as_f64(v).ok_or_else(|| err_bad(400, "concurrency 必须是 1 到 8 的整数"))?;
            if n.fract() != 0.0 || !(1.0..=8.0).contains(&n) {
                return Err(err_bad(400, "concurrency 必须是 1 到 8 的整数"));
            }
            n as usize
        }
    };
    Ok(TickReq {
        scene,
        agents,
        world,
        concurrency,
    })
}

fn validate_scene(input: &Value) -> Result<Scene, IntuitionError> {
    let raw = obj(input, "scene")?;
    Ok(Scene {
        place: require_text(raw.get("place"), "scene.place", 200)?,
        time_of_day: optional_text(raw.get("timeOfDay"), "scene.timeOfDay", 40)?,
        weather: optional_text(raw.get("weather"), "scene.weather", 80)?,
        current_directive: optional_text(
            raw.get("currentDirective"),
            "scene.currentDirective",
            64,
        )?,
        seconds_on_directive: optional_non_negative(
            raw.get("secondsOnDirective"),
            "scene.secondsOnDirective",
        )?,
        recent_events: string_list(raw.get("recentEvents"), "scene.recentEvents", 6, 180)?,
    })
}

fn validate_agent(input: &Value) -> Result<Agent, IntuitionError> {
    let raw = obj(input, "agent")?;
    Ok(Agent {
        id: require_id(raw.get("id"), "agent.id")?,
        role: require_text(raw.get("role"), "agent.role", 40)?,
        name: optional_text(raw.get("name"), "agent.name", 40)?,
        personality: optional_text(raw.get("personality"), "agent.personality", 280)?,
        goal: optional_text(raw.get("goal"), "agent.goal", 200)?,
        faction: optional_text(raw.get("faction"), "agent.faction", 40)?,
        activity: optional_text(raw.get("activity"), "agent.activity", 160)?,
        current_tactic: optional_text(raw.get("currentTactic"), "agent.currentTactic", 64)?,
        health: optional_unit(raw.get("health"), "agent.health")?,
        stamina: optional_unit(raw.get("stamina"), "agent.stamina")?,
        pos: optional_vec(raw.get("position"), "agent.position")?,
        current_target_id: optional_id(raw.get("currentTargetId"), "agent.currentTargetId")?,
        seconds_on_tactic: optional_non_negative(
            raw.get("secondsOnTactic"),
            "agent.secondsOnTactic",
        )?,
        memory: string_list(raw.get("memory"), "agent.memory", 4, 160)?,
    })
}

fn validate_nearby(input: Option<&Value>) -> Result<Vec<Entity>, IntuitionError> {
    match input {
        None | Some(Value::Null) => Ok(Vec::new()),
        Some(v) => {
            let list = v
                .as_array()
                .ok_or_else(|| err_bad(400, "nearby 必须是数组"))?;
            if list.len() > 12 {
                return Err(err_bad(
                    400,
                    "nearby 最多 12 个，请在游戏侧先筛掉远处的实体",
                ));
            }
            list.iter()
                .enumerate()
                .map(|(i, item)| validate_entity(item, &format!("nearby[{i}]"), "npc"))
                .collect()
        }
    }
}

fn validate_entity(
    input: &Value,
    path: &str,
    fallback_kind: &str,
) -> Result<Entity, IntuitionError> {
    let raw = obj(input, path)?;
    let kind = match raw.get("kind") {
        None | Some(Value::Null) => fallback_kind.to_string(),
        Some(v) => {
            let text = v.as_str().unwrap_or("");
            if !matches!(
                text,
                "player" | "npc" | "creature" | "prop" | "hazard" | "interest"
            ) {
                return Err(err_bad(
                    400,
                    format!("{path}.kind 必须是 player、npc、creature、prop、hazard、interest"),
                ));
            }
            text.to_string()
        }
    };
    let relation = match raw.get("relation") {
        None | Some(Value::Null) => String::new(),
        Some(v) => {
            let text = v.as_str().unwrap_or("");
            if !matches!(text, "ally" | "enemy" | "neutral" | "unknown") {
                return Err(err_bad(
                    400,
                    format!("{path}.relation 必须是 ally、enemy、neutral 或 unknown"),
                ));
            }
            text.to_string()
        }
    };
    Ok(Entity {
        id: require_id(raw.get("id"), &format!("{path}.id"))?,
        kind,
        relation,
        name: optional_text(raw.get("name"), &format!("{path}.name"), 40)?,
        faction: optional_text(raw.get("faction"), &format!("{path}.faction"), 40)?,
        activity: optional_text(raw.get("activity"), &format!("{path}.activity"), 160)?,
        distance: optional_non_negative(raw.get("distance"), &format!("{path}.distance"))?,
        health: optional_unit(raw.get("health"), &format!("{path}.health"))?,
        visible: match raw.get("visible") {
            None | Some(Value::Null) => None,
            Some(Value::Bool(b)) => Some(*b),
            Some(_) => return Err(err_bad(400, format!("{path}.visible 必须是布尔值"))),
        },
        pos: optional_vec(raw.get("position"), &format!("{path}.position"))?,
        tags: string_list(raw.get("tags"), &format!("{path}.tags"), 4, 32)?,
    })
}

fn validate_presences(input: Option<&Value>) -> Result<Vec<Presence>, IntuitionError> {
    match input {
        None | Some(Value::Null) => Ok(Vec::new()),
        Some(v) => {
            let list = v
                .as_array()
                .ok_or_else(|| err_bad(400, "presences 必须是数组"))?;
            if list.len() > 16 {
                return Err(err_bad(400, "presences 最多 16 个"));
            }
            list.iter()
                .enumerate()
                .map(|(i, item)| {
                    let raw = obj(item, &format!("presences[{i}]"))?;
                    Ok(Presence {
                        id: require_id(raw.get("id"), &format!("presences[{i}].id"))?,
                        role: require_text(raw.get("role"), &format!("presences[{i}].role"), 40)?,
                        activity: optional_text(
                            raw.get("activity"),
                            &format!("presences[{i}].activity"),
                            160,
                        )?,
                        faction: optional_text(
                            raw.get("faction"),
                            &format!("presences[{i}].faction"),
                            40,
                        )?,
                        health: optional_unit(
                            raw.get("health"),
                            &format!("presences[{i}].health"),
                        )?,
                    })
                })
                .collect()
        }
    }
}

fn validate_option_map(
    input: &Value,
    path: &str,
    min: usize,
    max: usize,
) -> Result<Vec<(String, String)>, IntuitionError> {
    let raw = obj(input, path)?;
    if raw.len() < min || raw.len() > max {
        return Err(err_bad(400, format!("{path} 需要 {min} 到 {max} 个选项")));
    }
    let mut out = Vec::new();
    for (key, value) in raw {
        if !valid_key(key) {
            return Err(err_bad(
                400,
                format!("{path}.{key} 的 id 需要以字母开头，只能含英文、数字和下划线"),
            ));
        }
        let Some(text) = value.as_str().map(str::trim).filter(|s| !s.is_empty()) else {
            return Err(err_bad(
                400,
                format!("{path}.{key} 需要一段情境描述，不能为空"),
            ));
        };
        if text.chars().count() > 500 {
            return Err(err_bad(400, format!("{path}.{key} 的描述超过 500 字")));
        }
        out.push((key.clone(), text.to_string()));
    }
    Ok(out)
}

fn policy_of(input: Option<&Value>) -> Result<Policy, IntuitionError> {
    match input {
        None | Some(Value::Null) => resolve_policy(None),
        Some(v) => resolve_policy(Some(obj(v, "policy")?)),
    }
}

fn scene_value(s: &Scene) -> Value {
    let mut out = Map::new();
    out.insert("place".into(), Value::String(s.place.clone()));
    put_str(&mut out, "timeOfDay", &s.time_of_day);
    put_str(&mut out, "weather", &s.weather);
    put_str(&mut out, "currentDirective", &s.current_directive);
    if let Some(n) = s.seconds_on_directive {
        out.insert("secondsOnDirective".into(), serde_json::json!(n));
    }
    if !s.recent_events.is_empty() {
        out.insert(
            "recentEvents".into(),
            Value::Array(s.recent_events.iter().cloned().map(Value::String).collect()),
        );
    }
    Value::Object(out)
}

fn obj<'a>(input: &'a Value, path: &str) -> Result<&'a Map<String, Value>, IntuitionError> {
    input
        .as_object()
        .ok_or_else(|| err_bad(400, format!("{path} 必须是对象")))
}

fn require_id(input: Option<&Value>, path: &str) -> Result<String, IntuitionError> {
    let text = input.and_then(|v| v.as_str()).unwrap_or("");
    if !valid_id(text) {
        return Err(err_bad(
            400,
            format!("{path} 需要 1 到 64 位的英文、数字、_ . : -"),
        ));
    }
    Ok(text.to_string())
}

fn optional_id(input: Option<&Value>, path: &str) -> Result<Option<String>, IntuitionError> {
    match input {
        None | Some(Value::Null) => Ok(None),
        Some(_) => Ok(Some(require_id(input, path)?)),
    }
}

fn require_text(input: Option<&Value>, path: &str, max: usize) -> Result<String, IntuitionError> {
    let text = input.and_then(|v| v.as_str()).unwrap_or("").trim();
    if text.is_empty() {
        return Err(err_bad(400, format!("{path} 不能为空")));
    }
    if text.chars().count() > max {
        return Err(err_bad(400, format!("{path} 超过 {max} 字")));
    }
    Ok(text.to_string())
}

fn optional_text(input: Option<&Value>, path: &str, max: usize) -> Result<String, IntuitionError> {
    match input {
        None | Some(Value::Null) => Ok(String::new()),
        Some(_) => require_text(input, path, max),
    }
}

fn optional_unit(input: Option<&Value>, path: &str) -> Result<Option<f64>, IntuitionError> {
    match input {
        None | Some(Value::Null) => Ok(None),
        Some(_) => Ok(Some(require_unit(input, path)?)),
    }
}

fn require_unit(input: Option<&Value>, path: &str) -> Result<f64, IntuitionError> {
    let value = require_finite(input, path)?;
    if !(0.0..=1.0).contains(&value) {
        return Err(err_bad(400, format!("{path} 必须在 0 到 1 之间")));
    }
    Ok(value)
}

fn optional_non_negative(input: Option<&Value>, path: &str) -> Result<Option<f64>, IntuitionError> {
    match input {
        None | Some(Value::Null) => Ok(None),
        Some(_) => Ok(Some(require_non_negative(input, path)?)),
    }
}

fn require_non_negative(input: Option<&Value>, path: &str) -> Result<f64, IntuitionError> {
    let value = require_finite(input, path)?;
    if value < 0.0 {
        return Err(err_bad(400, format!("{path} 不能是负数")));
    }
    Ok(value)
}

fn require_finite(input: Option<&Value>, path: &str) -> Result<f64, IntuitionError> {
    let value = input
        .and_then(as_f64)
        .filter(|n| n.is_finite())
        .ok_or_else(|| err_bad(400, format!("{path} 必须是有限数字")))?;
    Ok(value)
}

fn optional_vec(input: Option<&Value>, path: &str) -> Result<Option<Vec3>, IntuitionError> {
    match input {
        None | Some(Value::Null) => Ok(None),
        Some(v) => Ok(Some(validate_vec(v, path)?)),
    }
}

fn validate_vec(input: &Value, path: &str) -> Result<Vec3, IntuitionError> {
    let raw = obj(input, path)?;
    let z = match raw.get("z") {
        None | Some(Value::Null) => None,
        Some(_) => Some(require_finite(raw.get("z"), &format!("{path}.z"))?),
    };
    Ok(Vec3 {
        x: require_finite(raw.get("x"), &format!("{path}.x"))?,
        y: require_finite(raw.get("y"), &format!("{path}.y"))?,
        z,
    })
}

fn string_list(
    input: Option<&Value>,
    path: &str,
    max: usize,
    chars: usize,
) -> Result<Vec<String>, IntuitionError> {
    match input {
        None | Some(Value::Null) => Ok(Vec::new()),
        Some(v) => {
            let list = v
                .as_array()
                .ok_or_else(|| err_bad(400, format!("{path} 必须是字符串数组")))?;
            let start = list.len().saturating_sub(max);
            list[start..]
                .iter()
                .enumerate()
                .map(|(i, item)| require_text(Some(item), &format!("{path}[{}]", start + i), chars))
                .collect()
        }
    }
}

fn valid_id(s: &str) -> bool {
    let chars: Vec<char> = s.chars().collect();
    !chars.is_empty()
        && chars.len() <= 64
        && chars[0].is_ascii_alphanumeric()
        && chars
            .iter()
            .all(|c| c.is_ascii_alphanumeric() || matches!(c, '_' | '.' | ':' | '-'))
}

fn valid_key(s: &str) -> bool {
    let chars: Vec<char> = s.chars().collect();
    !chars.is_empty()
        && chars.len() <= 41
        && chars[0].is_ascii_alphabetic()
        && chars.iter().all(|c| c.is_ascii_alphanumeric() || *c == '_')
}

#[allow(dead_code)]
pub fn redact_error(err: &IntuitionError) -> String {
    redact(&err.message)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicBool, Ordering};

    fn guard(health: f64, tactic: &str, target: Option<&str>) -> Value {
        let mut agent = serde_json::json!({"id": "rook", "role": "guard", "health": health, "currentTactic": tactic, "secondsOnTactic": 4});
        if let Some(target) = target {
            agent["currentTargetId"] = Value::String(target.into());
        }
        serde_json::json!({
            "scene": {"place": "test yard"},
            "agent": agent,
            "player": {"id": "player", "kind": "player", "distance": 4, "visible": true, "health": 1, "relation": "neutral", "activity": "sprinting with a blade"},
            "nearby": [
                {"id": "wolf", "kind": "creature", "distance": 9, "health": 0.8, "relation": "enemy", "activity": "circling"},
                {"id": "mira", "kind": "npc", "faction": "chapel", "distance": 3, "health": 0.2, "relation": "ally", "activity": "on the ground"}
            ]
        })
    }

    fn answered(
        tactic: &str,
        confidence: f64,
        interrupt: f64,
        target: &str,
    ) -> impl Fn(&Value, &Value) -> Result<JudgeResult, IntuitionError> + Send + Sync {
        let tactic = tactic.to_string();
        let target = target.to_string();
        move |_, _| {
            let answers = serde_json::json!({
                "tactic": {"type": "choice", "choice": tactic, "probabilities": {tactic.clone(): 0.8, "hold": 0.2}},
                "target": {"type": "choice", "choice": target, "probabilities": {target.clone(): 0.9, "none": 0.1}},
                "threat": {"type": "score", "score": 2.2},
                "interrupt": {"type": "boolean", "probability": interrupt},
                "opening": {"type": "boolean", "probability": 0.2},
                "playerHostile": {"type": "boolean", "probability": 0.91},
                "allyNeedsHelp": {"type": "boolean", "probability": 0.2}
            }).as_object().unwrap().clone();
            Ok(JudgeResult {
                answers,
                provider_metadata: serde_json::json!({"typesafe": {"confidence": {"tactic": confidence, "threat": 0.8}}}).as_object().unwrap().clone(),
                model_id: "typesafe-ai/jev".into(),
                usage: None,
            })
        }
    }

    #[test]
    fn dead_agent_skips_judge() {
        let called = AtomicBool::new(false);
        let impulse = sense_agent(&guard(0.0, "patrol", Some("wolf")), &|_, _| {
            called.store(true, Ordering::SeqCst);
            Err(err_bad(500, "should not be called"))
        })
        .unwrap();
        assert!(!called.load(Ordering::SeqCst));
        assert_eq!(impulse.disposition, "incapacitated");
        assert_eq!(impulse.tactic, "none");
    }

    #[test]
    fn low_confidence_holds_patrol() {
        let impulse = sense_agent(
            &guard(1.0, "patrol", Some("wolf")),
            &answered("engage", 0.2, 0.1, "player"),
        )
        .unwrap();
        assert_eq!(impulse.suggested_tactic, "engage");
        assert_eq!(impulse.tactic, "patrol");
        assert_eq!(impulse.disposition, "hold");
        assert_eq!(impulse.because, "hysteresis");
        assert!(impulse.target_id.is_none());
        assert_eq!(impulse.player_hostile, Tri::True);
        assert_eq!(impulse.confidence_source, "typesafe");
    }

    #[test]
    fn high_confidence_locks_model_target() {
        let impulse = sense_agent(
            &guard(1.0, "patrol", None),
            &answered("engage", 0.9, 0.1, "player"),
        )
        .unwrap();
        assert_eq!(impulse.tactic, "engage");
        assert_eq!(impulse.because, "confident");
        assert_eq!(impulse.target_source, "model");
        assert_eq!(impulse.target_id.as_deref(), Some("player"));
    }

    #[test]
    fn continue_keeps_current_target() {
        let impulse = sense_agent(
            &guard(1.0, "engage", Some("wolf")),
            &answered("engage", 0.95, 0.1, "player"),
        )
        .unwrap();
        assert_eq!(impulse.disposition, "continue");
        assert_eq!(impulse.target_source, "kept");
        assert_eq!(impulse.target_id.as_deref(), Some("wolf"));
        assert_eq!(impulse.suggested_target_id.as_deref(), Some("player"));
    }

    #[test]
    fn world_holds_atmosphere() {
        let world = sense_world(&serde_json::json!({
            "scene": {"place": "chapel", "currentDirective": "hold_atmosphere", "secondsOnDirective": 10},
            "player": {"activity": "walking", "health": 1, "dominance": "passing"}
        }), &|_, _| Ok(JudgeResult {
            answers: serde_json::json!({
                "directive": {"type": "choice", "choice": "ambush_now", "probabilities": {"ambush_now": 0.55, "hold_atmosphere": 0.45}},
                "tension": {"type": "score", "score": 1.1},
                "overextended": {"type": "boolean", "probability": 0.2}
            }).as_object().unwrap().clone(),
            provider_metadata: serde_json::json!({"typesafe": {"confidence": {"directive": 0.3}}}).as_object().unwrap().clone(),
            model_id: "typesafe-ai/jev".into(),
            usage: None,
        })).unwrap();
        assert_eq!(world.directive, "hold_atmosphere");
        assert_eq!(world.suggested_directive, "ambush_now");
        assert_eq!(world.disposition, "hold");
        assert_eq!(world.player_overextended, Tri::False);
    }
}
