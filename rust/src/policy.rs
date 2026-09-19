use serde::Serialize;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Tri {
    False,
    True,
    Uncertain,
}

impl Serialize for Tri {
    fn serialize<S: serde::Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
        match self {
            Tri::True => serializer.serialize_bool(true),
            Tri::False => serializer.serialize_bool(false),
            Tri::Uncertain => serializer.serialize_str("uncertain"),
        }
    }
}

#[derive(Debug, Clone, Serialize)]
pub struct ScoreRead {
    pub score: f64,
    pub level: i32,
    pub label: String,
}

#[derive(Debug, Clone)]
pub struct Policy {
    pub switch_confidence: f64,
    pub interrupt_at: f64,
    pub uncertain_margin: f64,
}

impl Default for Policy {
    fn default() -> Self {
        Self {
            switch_confidence: 0.72,
            interrupt_at: 0.75,
            uncertain_margin: 0.12,
        }
    }
}

#[derive(Debug, Clone, PartialEq)]
pub struct Decision {
    pub disposition: String,
    pub executing: String,
    pub because: String,
}

#[derive(Debug, Clone)]
pub struct TargetCandidate {
    pub id: String,
    pub relation: String,
    pub distance: Option<f64>,
    pub health: Option<f64>,
    pub kind: String,
}

#[derive(Debug, Clone, PartialEq)]
pub struct Bound {
    pub target_id: Option<String>,
    pub target_source: String,
}

pub fn resolve_policy(
    input: Option<&serde_json::Map<String, serde_json::Value>>,
) -> Result<Policy, crate::error::IntuitionError> {
    let mut policy = Policy::default();
    let Some(input) = input else {
        return Ok(policy);
    };
    for key in ["switchConfidence", "interruptAt", "uncertainMargin"] {
        let Some(raw) = input.get(key) else { continue };
        if raw.is_null() {
            continue;
        }
        let Some(value) = as_f64(raw) else {
            return Err(crate::error::err_bad(
                400,
                format!("policy.{key} 必须是 0 到 1 之间的数字"),
            ));
        };
        if !value.is_finite() || !(0.0..=1.0).contains(&value) {
            return Err(crate::error::err_bad(
                400,
                format!("policy.{key} 必须是 0 到 1 之间的数字"),
            ));
        }
        match key {
            "switchConfidence" => policy.switch_confidence = value,
            "interruptAt" => policy.interrupt_at = value,
            _ => policy.uncertain_margin = value,
        }
    }
    Ok(policy)
}

pub fn classify_boolean(probability: Option<f64>, margin: f64) -> Tri {
    let Some(p) = probability.filter(|n| n.is_finite()) else {
        return Tri::Uncertain;
    };
    if p >= 0.5 + margin {
        Tri::True
    } else if p <= 0.5 - margin {
        Tri::False
    } else {
        Tri::Uncertain
    }
}

pub fn classify_interrupt(probability: Option<f64>, interrupt_at: f64) -> Tri {
    let Some(p) = probability.filter(|n| n.is_finite()) else {
        return Tri::Uncertain;
    };
    if p >= interrupt_at {
        Tri::True
    } else if p <= 1.0 - interrupt_at {
        Tri::False
    } else {
        Tri::Uncertain
    }
}

pub fn decide_disposition(
    current: &str,
    suggested: &str,
    confidence: Option<f64>,
    interrupt: Tri,
    hold_key: Option<&str>,
    switch_confidence: f64,
) -> Decision {
    let current = current.trim();
    let expanded = if hold_key == Some(suggested) && !current.is_empty() {
        current
    } else {
        suggested
    };
    if current.is_empty() {
        return Decision {
            disposition: "switch".into(),
            executing: expanded.into(),
            because: "first-decision".into(),
        };
    }
    if expanded == current {
        return Decision {
            disposition: "continue".into(),
            executing: current.into(),
            because: "still-fitting".into(),
        };
    }
    if interrupt == Tri::True {
        return Decision {
            disposition: "switch".into(),
            executing: expanded.into(),
            because: "interrupt".into(),
        };
    }
    if confidence.is_none_or(|c| c >= switch_confidence) {
        return Decision {
            disposition: "switch".into(),
            executing: expanded.into(),
            because: "confident".into(),
        };
    }
    Decision {
        disposition: "hold".into(),
        executing: current.into(),
        because: "hysteresis".into(),
    }
}

pub fn tactic_needs_target(tactic: &str) -> bool {
    !matches!(tactic, "hold" | "patrol" | "hide" | "flee")
}

pub fn bind_target(tactic: &str, model_target_id: &str, roster: &[TargetCandidate]) -> Bound {
    if !tactic_needs_target(tactic) {
        return Bound {
            target_id: None,
            target_source: "none".into(),
        };
    }
    if model_target_id != "none"
        && !model_target_id.is_empty()
        && roster.iter().any(|e| e.id == model_target_id)
    {
        return Bound {
            target_id: Some(model_target_id.into()),
            target_source: "model".into(),
        };
    }
    if let Some(id) = fallback_target(tactic, roster) {
        return Bound {
            target_id: Some(id),
            target_source: "geometric-fallback".into(),
        };
    }
    Bound {
        target_id: None,
        target_source: "none".into(),
    }
}

pub fn score_band(score: f64, labels: &[String]) -> ScoreRead {
    let max = labels.len().saturating_sub(1) as f64;
    let clamped = score.clamp(0.0, max);
    let mut level = clamped.round() as i32;
    level = level.clamp(0, max as i32);
    let label = labels
        .get(level as usize)
        .cloned()
        .unwrap_or_else(|| "unknown".into());
    ScoreRead {
        score: (clamped * 100.0).round() / 100.0,
        level,
        label,
    }
}

fn fallback_target(tactic: &str, roster: &[TargetCandidate]) -> Option<String> {
    let mut ranked = roster.to_vec();
    ranked.sort_by(|a, b| dist_or_far(a).total_cmp(&dist_or_far(b)));
    if tactic == "assist" {
        let mut allies: Vec<_> = ranked
            .into_iter()
            .filter(|e| e.relation == "ally")
            .collect();
        allies.sort_by(|a, b| {
            let ha = a.health.unwrap_or(1.0);
            let hb = b.health.unwrap_or(1.0);
            ha.total_cmp(&hb)
                .then(dist_or_far(a).total_cmp(&dist_or_far(b)))
        });
        return allies.first().map(|e| e.id.clone());
    }
    if tactic == "investigate" || tactic == "interact" {
        if let Some(found) = ranked
            .iter()
            .find(|e| matches!(e.kind.as_str(), "interest" | "hazard" | "prop"))
        {
            return Some(found.id.clone());
        }
        return ranked.first().map(|e| e.id.clone());
    }
    ranked
        .iter()
        .find(|e| e.relation == "enemy")
        .or_else(|| ranked.first())
        .map(|e| e.id.clone())
}

fn dist_or_far(item: &TargetCandidate) -> f64 {
    item.distance.unwrap_or(1e9)
}

pub fn as_f64(v: &serde_json::Value) -> Option<f64> {
    v.as_f64()
        .or_else(|| v.as_i64().map(|n| n as f64))
        .or_else(|| v.as_u64().map(|n| n as f64))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn low_confidence_holds() {
        let got = decide_disposition(
            "patrol",
            "engage",
            Some(0.4),
            Tri::False,
            Some("hold"),
            0.72,
        );
        assert_eq!(got.disposition, "hold");
        assert_eq!(got.executing, "patrol");
        assert_eq!(got.because, "hysteresis");
    }

    #[test]
    fn interrupt_switches() {
        let got = decide_disposition("patrol", "flee", Some(0.4), Tri::True, Some("hold"), 0.72);
        assert_eq!(got.executing, "flee");
        assert_eq!(got.because, "interrupt");
    }

    #[test]
    fn hold_expands() {
        let got = decide_disposition("patrol", "hold", Some(0.95), Tri::True, Some("hold"), 0.72);
        assert_eq!(got.executing, "patrol");
        assert_eq!(got.because, "still-fitting");
    }

    #[test]
    fn bind_skips_patrol() {
        assert_eq!(bind_target("patrol", "player", &[]).target_source, "none");
    }

    #[test]
    fn assist_weakest_ally() {
        let got = bind_target(
            "assist",
            "none",
            &[
                TargetCandidate {
                    id: "wolf".into(),
                    relation: "enemy".into(),
                    distance: Some(2.0),
                    health: Some(1.0),
                    kind: "creature".into(),
                },
                TargetCandidate {
                    id: "mira".into(),
                    relation: "ally".into(),
                    distance: Some(6.0),
                    health: Some(0.2),
                    kind: "npc".into(),
                },
                TargetCandidate {
                    id: "squire".into(),
                    relation: "ally".into(),
                    distance: Some(3.0),
                    health: Some(0.9),
                    kind: "npc".into(),
                },
            ],
        );
        assert_eq!(got.target_id.as_deref(), Some("mira"));
        assert_eq!(got.target_source, "geometric-fallback");
    }
}
