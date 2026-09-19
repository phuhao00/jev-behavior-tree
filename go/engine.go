package main

import (
	"encoding/json"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

type ScoreRead struct {
	Score float64 `json:"score"`
	Level int     `json:"level"`
	Label string  `json:"label"`
}

type TokenUsage struct {
	InputTokens  *float64 `json:"inputTokens,omitempty"`
	OutputTokens *float64 `json:"outputTokens,omitempty"`
	TotalTokens  *float64 `json:"totalTokens,omitempty"`
}

type probMap map[string]float64

func (p probMap) MarshalJSON() ([]byte, error) {
	if p == nil {
		return []byte("null"), nil
	}
	return json.Marshal(map[string]float64(p))
}

type Impulse struct {
	SchemaVersion     int         `json:"schemaVersion"`
	AgentID           string      `json:"agentId"`
	Role              string      `json:"role"`
	Disposition       string      `json:"disposition"`
	Tactic            string      `json:"tactic"`
	SuggestedTactic   string      `json:"suggestedTactic"`
	TargetID          *string     `json:"targetId"`
	SuggestedTargetID *string     `json:"suggestedTargetId"`
	TargetSource      string      `json:"targetSource"`
	Because           string      `json:"because"`
	Interrupt         Tri         `json:"interrupt"`
	Opening           Tri         `json:"opening"`
	OpeningKind       string      `json:"openingKind"`
	PlayerHostile     Tri         `json:"playerHostile"`
	AllyNeedsHelp     Tri         `json:"allyNeedsHelp"`
	Threat            ScoreRead   `json:"threat"`
	Confidence        *float64    `json:"confidence"`
	ConfidenceSource  string      `json:"confidenceSource"`
	Probabilities     probMap     `json:"probabilities"`
	ModelID           string      `json:"modelId"`
	Usage             *TokenUsage `json:"usage,omitempty"`
	LatencyMs         int64       `json:"latencyMs"`
}

type WorldImpulse struct {
	SchemaVersion      int         `json:"schemaVersion"`
	Disposition        string      `json:"disposition"`
	Directive          string      `json:"directive"`
	SuggestedDirective string      `json:"suggestedDirective"`
	Because            string      `json:"because"`
	Tension            ScoreRead   `json:"tension"`
	PlayerOverextended Tri         `json:"playerOverextended"`
	Confidence         *float64    `json:"confidence"`
	ConfidenceSource   string      `json:"confidenceSource"`
	Probabilities      probMap     `json:"probabilities"`
	ModelID            string      `json:"modelId"`
	Usage              *TokenUsage `json:"usage,omitempty"`
	LatencyMs          int64       `json:"latencyMs"`
}

type vec3 struct {
	X, Y float64
	Z    *float64
}

type entity struct {
	ID, Kind, Name, Faction, Relation, Activity string
	Distance, Health                            *float64
	Visible                                     *bool
	Tags                                        []string
	Pos                                         *vec3
}

type scene struct {
	Place, TimeOfDay, Weather, CurrentDirective string
	SecondsOnDirective                          *float64
	RecentEvents                                []string
}

type agent struct {
	ID, Role, Name, Personality, Goal, Faction, Activity, CurrentTactic string
	Health, Stamina, SecondsOnTactic                                    *float64
	CurrentTargetID                                                     *string
	Memory                                                              []string
	Pos                                                                 *vec3
}

type presence struct {
	ID, Role, Activity, Faction string
	Health                      *float64
}

type worldPlayer struct {
	Activity, Dominance string
	Health              *float64
}

type senseReq struct {
	Scene   scene
	Agent   agent
	Nearby  []entity
	Player  *entity
	Tactics map[string]string
	Policy  Policy
}

type worldReq struct {
	Scene      scene
	Presences  []presence
	Player     *worldPlayer
	Directives map[string]string
	Policy     Policy
}

type tickReq struct {
	Scene       scene
	Agents      []senseReq
	World       *worldReq
	Concurrency int
}

type rosterEntry struct {
	ID, ChoiceKey, Kind, Name, Faction, Relation, Activity string
	Distance, Health                                       *float64
	Visible                                                *bool
	Tags                                                   []string
}

type compactAgent struct {
	State     map[string]any
	Roster    []rosterEntry
	HasPlayer bool
}

type Judge func(state any, questions map[string]any) (judgeResult, error)

type judgeResult struct {
	Answers          map[string]any
	ProviderMetadata map[string]any
	ModelID          string
	Usage            *TokenUsage
}

func senseAgent(input any, judge Judge) (Impulse, error) {
	req, err := validateSense(input)
	if err != nil {
		return Impulse{}, err
	}
	return senseAgentReq(req, judge)
}

func senseAgentReq(req senseReq, judge Judge) (Impulse, error) {
	started := time.Now()
	if req.Agent.Health != nil && *req.Agent.Health <= 0 {
		return incapacitated(req, started), nil
	}
	compact := compactSense(req)
	askAlly := false
	for _, entry := range compact.Roster {
		if entry.Relation == "ally" {
			askAlly = true
			break
		}
	}
	questions := buildAgentQuestions(req.Tactics, compact.Roster, req.Agent.Role, compact.HasPlayer, askAlly)
	result, err := judge(compact.State, questions)
	if err != nil {
		return Impulse{}, err
	}
	return composeAgent(req, compact, result, started, askAlly)
}

func senseWorld(input any, judge Judge) (WorldImpulse, error) {
	req, err := validateWorld(input)
	if err != nil {
		return WorldImpulse{}, err
	}
	return senseWorldReq(req, judge)
}

func senseWorldReq(req worldReq, judge Judge) (WorldImpulse, error) {
	started := time.Now()
	result, err := judge(compactWorld(req), buildWorldQuestions(req.Directives, req.Player != nil))
	if err != nil {
		return WorldImpulse{}, err
	}
	return composeWorld(req, result, started)
}

func senseTick(input any, judge Judge) (map[string]any, error) {
	started := time.Now()
	req, err := validateTick(input)
	if err != nil {
		return nil, err
	}
	type job struct {
		world bool
		index int
		run   func() (any, error)
	}
	jobs := make([]job, 0, len(req.Agents)+1)
	if req.World != nil {
		worldReq := *req.World
		jobs = append(jobs, job{world: true, run: func() (any, error) { return senseWorldReq(worldReq, judge) }})
	}
	for i := range req.Agents {
		agent := req.Agents[i]
		idx := i
		jobs = append(jobs, job{index: idx, run: func() (any, error) { return senseAgentReq(agent, judge) }})
	}
	agents := make([]any, len(req.Agents))
	var world any
	var mu sync.Mutex
	sem := make(chan struct{}, req.Concurrency)
	done := make(chan struct{}, len(jobs))
	for _, item := range jobs {
		item := item
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; done <- struct{}{} }()
			value, callErr := item.run()
			mu.Lock()
			defer mu.Unlock()
			if item.world {
				if callErr != nil {
					world = map[string]any{"error": callErr.Error(), "status": statusOf(callErr)}
				} else {
					world = value
				}
				return
			}
			if callErr != nil {
				agents[item.index] = map[string]any{"agentId": req.Agents[item.index].Agent.ID, "error": callErr.Error(), "status": statusOf(callErr)}
				return
			}
			agents[item.index] = value
		}()
	}
	for range jobs {
		<-done
	}
	return map[string]any{
		"schemaVersion": 1,
		"scene":         map[string]any{"place": req.Scene.Place},
		"world":         world,
		"agents":        agents,
		"latencyMs":     time.Since(started).Milliseconds(),
	}, nil
}

func composeAgent(req senseReq, compact compactAgent, result judgeResult, started time.Time, askAlly bool) (Impulse, error) {
	tactic, err := requireChoice(result.Answers, "tactic")
	if err != nil {
		return Impulse{}, err
	}
	if _, ok := req.Tactics[tactic.choice]; !ok {
		return Impulse{}, errBad(502, "Jev 返回了未声明的战术 "+tactic.choice)
	}
	threat, err := requireScore(result.Answers, "threat")
	if err != nil {
		return Impulse{}, err
	}
	interrupt := classifyInterrupt(readProbability(result.Answers, "interrupt"), req.Policy.InterruptAt)
	confidence, source := resolveConfidence(tactic.confidence, result.ProviderMetadata, "tactic", tactic.probabilities)
	var holdKey *string
	if _, ok := req.Tactics[holdTactic]; ok {
		key := holdTactic
		holdKey = &key
	}
	decision := decideDisposition(req.Agent.CurrentTactic, tactic.choice, confidence, interrupt, holdKey, req.Policy.SwitchConfidence)
	modelTarget := modelTargetID(result.Answers, compact.Roster)
	candidates := candidatesOf(compact.Roster)
	bound := bindTarget(decision.Executing, modelTarget, candidates)
	if kept := keepCurrentTarget(decision.Disposition, decision.Executing, req.Agent.CurrentTargetID, compact.Roster); kept != nil {
		bound = *kept
	}
	suggestedName := tactic.choice
	if holdKey != nil && tactic.choice == *holdKey && req.Agent.CurrentTactic != "" {
		suggestedName = req.Agent.CurrentTactic
	}
	suggested := bindTarget(suggestedName, modelTarget, candidates)
	opening := classifyBoolean(readProbability(result.Answers, "opening"), req.Policy.UncertainMargin)
	playerHostile := TriFalse
	if compact.HasPlayer {
		playerHostile = classifyBoolean(readProbability(result.Answers, "playerHostile"), req.Policy.UncertainMargin)
	}
	ally := TriFalse
	if askAlly {
		ally = classifyBoolean(readProbability(result.Answers, "allyNeedsHelp"), req.Policy.UncertainMargin)
	}
	model := result.ModelID
	if model == "" {
		model = modelID()
	}
	return Impulse{
		SchemaVersion: 1, AgentID: req.Agent.ID, Role: req.Agent.Role,
		Disposition: decision.Disposition, Tactic: decision.Executing, SuggestedTactic: tactic.choice,
		TargetID: bound.TargetID, SuggestedTargetID: suggested.TargetID, TargetSource: bound.TargetSource,
		Because: decision.Because, Interrupt: interrupt, Opening: opening, OpeningKind: openingKindFor(req.Agent.Role),
		PlayerHostile: playerHostile, AllyNeedsHelp: ally, Threat: scoreBand(threat, threatLevels()),
		Confidence: confidence, ConfidenceSource: source, Probabilities: tactic.probabilities,
		ModelID: model, Usage: result.Usage, LatencyMs: time.Since(started).Milliseconds(),
	}, nil
}

func composeWorld(req worldReq, result judgeResult, started time.Time) (WorldImpulse, error) {
	directive, err := requireChoice(result.Answers, "directive")
	if err != nil {
		return WorldImpulse{}, err
	}
	if _, ok := req.Directives[directive.choice]; !ok {
		return WorldImpulse{}, errBad(502, "Jev 返回了未声明的拍子 "+directive.choice)
	}
	tension, err := requireScore(result.Answers, "tension")
	if err != nil {
		return WorldImpulse{}, err
	}
	confidence, source := resolveConfidence(directive.confidence, result.ProviderMetadata, "directive", directive.probabilities)
	var holdKey *string
	if _, ok := req.Directives[holdDirective]; ok {
		key := holdDirective
		holdKey = &key
	}
	decision := decideDisposition(req.Scene.CurrentDirective, directive.choice, confidence, TriFalse, holdKey, req.Policy.SwitchConfidence)
	over := TriFalse
	if req.Player != nil {
		over = classifyBoolean(readProbability(result.Answers, "overextended"), req.Policy.UncertainMargin)
	}
	model := result.ModelID
	if model == "" {
		model = modelID()
	}
	return WorldImpulse{
		SchemaVersion: 1, Disposition: decision.Disposition, Directive: decision.Executing,
		SuggestedDirective: directive.choice, Because: decision.Because, Tension: scoreBand(tension, tensionLevels()),
		PlayerOverextended: over, Confidence: confidence, ConfidenceSource: source, Probabilities: directive.probabilities,
		ModelID: model, Usage: result.Usage, LatencyMs: time.Since(started).Milliseconds(),
	}, nil
}

func incapacitated(req senseReq, started time.Time) Impulse {
	return Impulse{
		SchemaVersion: 1, AgentID: req.Agent.ID, Role: req.Agent.Role, Disposition: "incapacitated",
		Tactic: "none", SuggestedTactic: "none", TargetSource: "none", Because: "incapacitated",
		Interrupt: TriFalse, Opening: TriFalse, OpeningKind: openingKindFor(req.Agent.Role),
		PlayerHostile: TriFalse, AllyNeedsHelp: TriFalse, Threat: scoreBand(3, threatLevels()),
		ConfidenceSource: "none", ModelID: "skipped", LatencyMs: time.Since(started).Milliseconds(),
	}
}

type choiceAnswer struct {
	choice        string
	probabilities probMap
	confidence    *float64
}

func requireChoice(answers map[string]any, id string) (choiceAnswer, error) {
	raw, _ := answers[id].(map[string]any)
	choice, _ := raw["choice"].(string)
	if choice == "" {
		keys := make([]string, 0, len(answers))
		for key := range answers {
			keys = append(keys, key)
		}
		return choiceAnswer{}, errBad(502, "Jev 没有返回 "+id+"。答案键："+strings.Join(keys, ", "))
	}
	return choiceAnswer{choice: choice, probabilities: readDistribution(raw["probabilities"]), confidence: readUnit(raw["confidence"])}, nil
}

func requireScore(answers map[string]any, id string) (float64, error) {
	raw, _ := answers[id].(map[string]any)
	score, ok := asFloat(raw["score"])
	if !ok || math.IsNaN(score) {
		return 0, errBad(502, "Jev 没有返回 "+id)
	}
	return score, nil
}

func readProbability(answers map[string]any, id string) *float64 {
	raw, _ := answers[id].(map[string]any)
	if raw == nil {
		return nil
	}
	if p := readUnit(raw["probability"]); p != nil {
		return p
	}
	return readUnit(raw["noul"])
}

func readUnit(v any) *float64 {
	n, ok := asFloat(v)
	if !ok || math.IsNaN(n) || n < 0 || n > 1 {
		return nil
	}
	return &n
}

func readDistribution(v any) probMap {
	raw, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := probMap{}
	for key, item := range raw {
		if n, ok := asFloat(item); ok && !math.IsNaN(n) {
			out[key] = n
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func resolveConfidence(answer *float64, metadata map[string]any, id string, probabilities probMap) (*float64, string) {
	if answer != nil {
		return answer, "answer"
	}
	if meta := readMetaConfidence(metadata, id); meta != nil {
		return meta, "typesafe"
	}
	if margin := confidenceFromDistribution(probabilities); margin != nil {
		return margin, "margin"
	}
	return nil, "none"
}

func readMetaConfidence(metadata map[string]any, id string) *float64 {
	if metadata == nil {
		return nil
	}
	buckets := []any{metadata["typesafe"], metadata["typesafe-ai"]}
	if gateway, ok := metadata["gateway"].(map[string]any); ok {
		buckets = append(buckets, gateway["typesafe"])
	}
	for _, bucket := range buckets {
		rec, _ := bucket.(map[string]any)
		conf, _ := rec["confidence"].(map[string]any)
		if value := readUnit(conf[id]); value != nil {
			return value
		}
	}
	return nil
}

func confidenceFromDistribution(probabilities probMap) *float64 {
	if len(probabilities) == 0 {
		return nil
	}
	top, second := -1.0, 0.0
	for _, value := range probabilities {
		if value > top {
			second = top
			top = value
		} else if value > second {
			second = value
		}
	}
	if top < 0 {
		return nil
	}
	if second < 0 {
		second = 0
	}
	margin := math.Round((top-second)*100) / 100
	return &margin
}

func modelTargetID(answers map[string]any, roster []rosterEntry) string {
	target, err := requireChoice(answers, "target")
	if err != nil || target.choice == "none" {
		return "none"
	}
	for _, entry := range roster {
		if entry.ChoiceKey == target.choice {
			return entry.ID
		}
	}
	return "none"
}

func candidatesOf(roster []rosterEntry) []TargetCandidate {
	out := make([]TargetCandidate, len(roster))
	for i, entry := range roster {
		out[i] = TargetCandidate{ID: entry.ID, Relation: entry.Relation, Distance: entry.Distance, Health: entry.Health, Kind: entry.Kind}
	}
	return out
}

func keepCurrentTarget(disposition, tactic string, current *string, roster []rosterEntry) *Bound {
	if disposition != "continue" && disposition != "hold" {
		return nil
	}
	if !tacticNeedsTarget(tactic) || current == nil || *current == "" {
		return nil
	}
	for _, entry := range roster {
		if entry.ID == *current {
			id := *current
			return &Bound{TargetID: &id, TargetSource: "kept"}
		}
	}
	return nil
}

func compactSense(req senseReq) compactAgent {
	personality, goal, hasPrior := priorForRole(req.Agent.Role)
	if req.Agent.Personality == "" && hasPrior {
		req.Agent.Personality = personality
	}
	if req.Agent.Goal == "" && hasPrior {
		req.Agent.Goal = goal
	}
	roster := buildRoster(req.Agent, req.Player, req.Nearby)
	var playerEntry *rosterEntry
	var nearby []map[string]any
	for i := range roster {
		entry := roster[i]
		if req.Player != nil && entry.ID == req.Player.ID {
			copy := entry
			playerEntry = &copy
			continue
		}
		nearby = append(nearby, publicEntry(entry))
	}
	state := map[string]any{}
	if scene := omitScene(req.Scene, false); len(scene) > 0 {
		state["scene"] = scene
	}
	state["agent"] = omitAgent(req.Agent)
	if playerEntry != nil {
		state["player"] = publicEntry(*playerEntry)
	}
	if len(nearby) > 0 {
		state["nearby"] = nearby
	}
	return compactAgent{State: state, Roster: roster, HasPlayer: playerEntry != nil}
}

func compactWorld(req worldReq) map[string]any {
	state := map[string]any{}
	if scene := omitScene(req.Scene, true); len(scene) > 0 {
		state["scene"] = scene
	}
	if req.Player != nil {
		player := map[string]any{}
		putMap(player, "activity", req.Player.Activity)
		if req.Player.Health != nil {
			player["health"] = *req.Player.Health
		}
		putMap(player, "dominance", req.Player.Dominance)
		if len(player) > 0 {
			state["player"] = player
		}
	}
	if len(req.Presences) > 0 {
		rows := make([]any, 0, len(req.Presences))
		for _, item := range req.Presences {
			row := map[string]any{"id": item.ID, "role": item.Role}
			putMap(row, "activity", item.Activity)
			putMap(row, "faction", item.Faction)
			if item.Health != nil {
				row["health"] = *item.Health
			}
			rows = append(rows, row)
		}
		state["presences"] = rows
	}
	return state
}

func omitScene(s scene, world bool) map[string]any {
	out := map[string]any{}
	putMap(out, "place", s.Place)
	putMap(out, "timeOfDay", s.TimeOfDay)
	putMap(out, "weather", s.Weather)
	if world {
		putMap(out, "currentDirective", s.CurrentDirective)
		if s.SecondsOnDirective != nil {
			out["secondsOnDirective"] = *s.SecondsOnDirective
		}
	}
	if len(s.RecentEvents) > 0 {
		out["recentEvents"] = s.RecentEvents
	}
	return out
}

func omitAgent(a agent) map[string]any {
	out := map[string]any{"id": a.ID, "role": a.Role}
	putMap(out, "name", a.Name)
	putMap(out, "personality", a.Personality)
	putMap(out, "goal", a.Goal)
	putMap(out, "faction", a.Faction)
	putMap(out, "activity", a.Activity)
	putMap(out, "currentTactic", a.CurrentTactic)
	if a.Health != nil {
		out["health"] = *a.Health
	}
	if a.Stamina != nil {
		out["stamina"] = *a.Stamina
	}
	if a.CurrentTargetID != nil && *a.CurrentTargetID != "" {
		out["currentTargetId"] = *a.CurrentTargetID
	}
	if a.SecondsOnTactic != nil {
		out["secondsOnTactic"] = *a.SecondsOnTactic
	}
	if len(a.Memory) > 0 {
		out["memory"] = a.Memory
	}
	return out
}

func putMap(m map[string]any, key, value string) {
	if value != "" {
		m[key] = value
	}
}

func buildRoster(a agent, player *entity, nearby []entity) []rosterEntry {
	rows := []entity{}
	if player != nil && player.ID != a.ID {
		rows = append(rows, *player)
	}
	sorted := append([]entity(nil), nearby...)
	for i := 1; i < len(sorted); i++ {
		j := i
		for j > 0 && measured(a, sorted[j]) < measured(a, sorted[j-1]) {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
			j--
		}
	}
	for _, item := range sorted {
		if item.ID == a.ID || (player != nil && item.ID == player.ID) {
			continue
		}
		rows = append(rows, item)
		if len(rows) >= 16 {
			break
		}
	}
	if len(rows) > 16 {
		rows = rows[:16]
	}
	used := map[string]struct{}{"none": {}}
	out := make([]rosterEntry, 0, len(rows))
	for _, item := range rows {
		out = append(out, toEntry(a, item, used))
	}
	return out
}

func toEntry(a agent, item entity, used map[string]struct{}) rosterEntry {
	distance := measured(a, item)
	var dist *float64
	if distance < 1e8 {
		rounded := math.Round(distance*10) / 10
		dist = &rounded
	}
	name := item.Name
	if name == "" {
		name = item.ID
	}
	activity := item.Activity
	if utfLen(activity) > 120 {
		activity = clip(activity, 120)
	}
	tags := item.Tags
	if len(tags) > 4 {
		tags = tags[:4]
	}
	return rosterEntry{
		ID: item.ID, ChoiceKey: choiceKey(item.ID, used), Kind: item.Kind, Name: name,
		Faction: item.Faction, Relation: relationOf(a, item), Distance: dist, Visible: item.Visible,
		Health: item.Health, Activity: activity, Tags: tags,
	}
}

func relationOf(a agent, item entity) string {
	if item.Relation != "" {
		return item.Relation
	}
	if a.Faction != "" && item.Faction != "" && a.Faction == item.Faction {
		return "ally"
	}
	return "unknown"
}

func measured(a agent, item entity) float64 {
	if item.Distance != nil {
		return *item.Distance
	}
	if a.Pos == nil || item.Pos == nil {
		return 1e9
	}
	az, bz := 0.0, 0.0
	if a.Pos.Z != nil {
		az = *a.Pos.Z
	}
	if item.Pos.Z != nil {
		bz = *item.Pos.Z
	}
	return math.Hypot(a.Pos.X-item.Pos.X, math.Hypot(a.Pos.Y-item.Pos.Y, az-bz))
}

func choiceKey(id string, used map[string]struct{}) string {
	var b strings.Builder
	for _, r := range id {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	base := strings.TrimLeftFunc(b.String(), func(r rune) bool { return !unicode.IsLetter(r) })
	if utfLen(base) > 40 {
		base = string([]rune(base)[:40])
	}
	if base == "" || base == "none" {
		base = "entity"
	}
	key := base
	for n := 2; ; n++ {
		if _, ok := used[key]; !ok {
			used[key] = struct{}{}
			return key
		}
		key = base + "_" + itoa(n)
	}
}

func publicEntry(entry rosterEntry) map[string]any {
	out := map[string]any{"id": entry.ChoiceKey, "kind": entry.Kind, "relation": entry.Relation}
	if entry.Name != "" && entry.Name != entry.ChoiceKey {
		out["name"] = entry.Name
	}
	putMap(out, "faction", entry.Faction)
	if entry.Distance != nil {
		out["distance"] = *entry.Distance
	}
	if entry.Visible != nil {
		out["visible"] = *entry.Visible
	}
	if entry.Health != nil {
		out["health"] = *entry.Health
	}
	putMap(out, "activity", entry.Activity)
	if len(entry.Tags) > 0 {
		out["tags"] = entry.Tags
	}
	return out
}

func buildAgentQuestions(tactics map[string]string, roster []rosterEntry, role string, askPlayer, askAlly bool) map[string]any {
	instructions, criteria := openingQuestion(role)
	questions := map[string]any{
		"tactic": map[string]any{
			"type":         "choice",
			"instructions": "Which single tactic should this agent commit to now? Use `agent.role`, `agent.personality`, `agent.goal`, `agent.health`, `agent.currentTactic`, `agent.activity`, `scene`, `player`, and `nearby`. Choose hold when the current tactic still matches the moment. Do not invent a tactic.",
			"criteria":     tactics,
		},
		"threat": map[string]any{
			"type":         "score",
			"instructions": "How much danger is this agent in right now? Match `agent.health`, `player`, and `nearby` to a situation. Score this agent, not the whole scene.",
			"criteria":     threatLevels(),
		},
		"interrupt": map[string]any{
			"type":         "boolean",
			"instructions": "Should this agent abort `agent.currentTactic` immediately? Use `agent.secondsOnTactic`, `agent.health`, `player`, and `nearby`.",
			"criteria": map[string]string{
				"true":  "The assumption behind the current tactic just broke: a new threat, a dying ally, or the target is gone.",
				"false": "The current tactic still fits, or there is no current tactic that needs aborting.",
			},
		},
		"opening": map[string]any{"type": "boolean", "instructions": instructions, "criteria": criteria},
	}
	if len(roster) > 0 {
		criteria := map[string]string{"none": "No specific entity. The moment is about the place or the self, not a lock-on."}
		for _, entry := range roster {
			criteria[entry.ChoiceKey] = describeEntry(entry)
		}
		questions["target"] = map[string]any{
			"type":         "choice",
			"instructions": "Which entity is the focus of this moment? Choose none when the agent should not lock onto anyone. The option id matches `id` on `player` or `nearby`.",
			"criteria":     criteria,
		}
	}
	if askPlayer {
		questions["playerHostile"] = map[string]any{
			"type":         "boolean",
			"instructions": "Is `player` about to attack this agent or an ally, as opposed to passing by, talking, or leaving?",
			"criteria": map[string]string{
				"true":  "A weapon is out, they are sprinting in, they just struck, or they are clearly hunting.",
				"false": "Sheathed, idle, talking, leaving, or moving past without a threat.",
			},
		}
	}
	if askAlly {
		questions["allyNeedsHelp"] = map[string]any{
			"type":         "boolean",
			"instructions": "Does an ally in `nearby` need this agent's help within the next few seconds?",
			"criteria": map[string]string{
				"true":  "An ally is hurt, falling, cornered, or calling for help.",
				"false": "Allies are fine, or none of them need this agent.",
			},
		}
	}
	return questions
}

func buildWorldQuestions(directives map[string]string, askPlayer bool) map[string]any {
	questions := map[string]any{
		"directive": map[string]any{
			"type":         "choice",
			"instructions": "Which single beat should this place play now? Use `scene.currentDirective`, `scene.recentEvents`, `player`, and `presences`. Choose hold_atmosphere when the current beat still fits. Do not invent a beat.",
			"criteria":     directives,
		},
		"tension": map[string]any{
			"type":         "score",
			"instructions": "Where does the place sit right now, as a situation rather than a vague intensity? Use `scene`, `player`, and `presences`.",
			"criteria":     tensionLevels(),
		},
	}
	if askPlayer {
		questions["overextended"] = map[string]any{
			"type":         "boolean",
			"instructions": "Is the player overextended: too deep, too hurt, or too committed, so the place could punish them?",
			"criteria": map[string]string{
				"true":  "The player is deep in, badly hurt, surrounded, or cut off from an easy step back.",
				"false": "The player has room, health, or an obvious way to step back.",
			},
		}
	}
	return questions
}

func describeEntry(entry rosterEntry) string {
	parts := []string{}
	if entry.Name != "" && entry.Name != entry.ID {
		parts = append(parts, entry.Name+" ("+entry.ID+")")
	} else {
		parts = append(parts, entry.ID)
	}
	parts = append(parts, entry.Kind, entry.Relation)
	if entry.Distance != nil {
		parts = append(parts, trimFloat(*entry.Distance)+"m")
	} else {
		parts = append(parts, "distance unknown")
	}
	switch {
	case entry.Visible != nil && *entry.Visible:
		parts = append(parts, "visible")
	case entry.Visible != nil:
		parts = append(parts, "not visible")
	default:
		parts = append(parts, "visibility unknown")
	}
	if entry.Health != nil {
		parts = append(parts, "health "+trimFloat(*entry.Health))
	}
	if entry.Activity != "" {
		parts = append(parts, entry.Activity)
	}
	if len(entry.Tags) > 0 {
		parts = append(parts, "tags "+strings.Join(entry.Tags, ", "))
	}
	return strings.Join(parts, ", ")
}

var (
	idPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$`)
	keyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,40}$`)
	kinds      = map[string]struct{}{"player": {}, "npc": {}, "creature": {}, "prop": {}, "hazard": {}, "interest": {}}
	relations  = map[string]struct{}{"ally": {}, "enemy": {}, "neutral": {}, "unknown": {}}
)

func validateSense(input any) (senseReq, error) {
	raw, err := asObject(input, "请求体")
	if err != nil {
		return senseReq{}, err
	}
	scene, err := validateScene(raw["scene"])
	if err != nil {
		return senseReq{}, err
	}
	agent, err := validateAgent(raw["agent"])
	if err != nil {
		return senseReq{}, err
	}
	nearby, err := validateNearby(raw["nearby"])
	if err != nil {
		return senseReq{}, err
	}
	var player *entity
	if raw["player"] != nil {
		parsed, err := validateEntity(raw["player"], "player", "player")
		if err != nil {
			return senseReq{}, err
		}
		player = &parsed
	}
	var tactics map[string]string
	if raw["tactics"] == nil {
		tactics = tacticsForRole(agent.Role)
		if tactics == nil {
			return senseReq{}, errBad(400, `角色 "`+agent.Role+`" 没有预置战术。请传 tactics。预置角色：guard、civilian、predator、companion、ambient`)
		}
	} else {
		tactics, err = validateOptionMap(raw["tactics"], "tactics", 1, 24)
		if err != nil {
			return senseReq{}, err
		}
	}
	policyInput, _ := raw["policy"].(map[string]any)
	if raw["policy"] != nil && policyInput == nil {
		return senseReq{}, errBad(400, "policy 必须是对象")
	}
	policy, err := resolvePolicy(policyInput)
	if err != nil {
		return senseReq{}, err
	}
	return senseReq{Scene: scene, Agent: agent, Nearby: nearby, Player: player, Tactics: tactics, Policy: policy}, nil
}

func validateWorld(input any) (worldReq, error) {
	raw, err := asObject(input, "world")
	if err != nil {
		return worldReq{}, err
	}
	scene, err := validateScene(raw["scene"])
	if err != nil {
		return worldReq{}, err
	}
	presences, err := validatePresences(raw["presences"])
	if err != nil {
		return worldReq{}, err
	}
	var player *worldPlayer
	if raw["player"] != nil {
		obj, err := asObject(raw["player"], "player")
		if err != nil {
			return worldReq{}, err
		}
		wp := worldPlayer{}
		if obj["activity"] != nil {
			wp.Activity, err = requireText(obj["activity"], "player.activity", 160)
			if err != nil {
				return worldReq{}, err
			}
		}
		if obj["dominance"] != nil {
			wp.Dominance, err = requireText(obj["dominance"], "player.dominance", 160)
			if err != nil {
				return worldReq{}, err
			}
		}
		if obj["health"] != nil {
			h, err := requireUnit(obj["health"], "player.health")
			if err != nil {
				return worldReq{}, err
			}
			wp.Health = &h
		}
		player = &wp
	}
	directives := worldDirectives()
	if raw["directives"] != nil {
		directives, err = validateOptionMap(raw["directives"], "directives", 1, 24)
		if err != nil {
			return worldReq{}, err
		}
	}
	policyInput, _ := raw["policy"].(map[string]any)
	if raw["policy"] != nil && policyInput == nil {
		return worldReq{}, errBad(400, "policy 必须是对象")
	}
	policy, err := resolvePolicy(policyInput)
	if err != nil {
		return worldReq{}, err
	}
	return worldReq{Scene: scene, Presences: presences, Player: player, Directives: directives, Policy: policy}, nil
}

func validateTick(input any) (tickReq, error) {
	raw, err := asObject(input, "请求体")
	if err != nil {
		return tickReq{}, err
	}
	scene, err := validateScene(raw["scene"])
	if err != nil {
		return tickReq{}, err
	}
	var agentsRaw []any
	if raw["agents"] != nil {
		list, ok := raw["agents"].([]any)
		if !ok {
			return tickReq{}, errBad(400, "agents 必须是数组")
		}
		agentsRaw = list
	}
	if len(agentsRaw) > 16 {
		return tickReq{}, errBad(400, "单次 tick 最多 16 个 agent")
	}
	agents := make([]senseReq, len(agentsRaw))
	for i, item := range agentsRaw {
		obj, err := asObject(item, "agents["+itoa(i)+"]")
		if err != nil {
			return tickReq{}, err
		}
		obj["scene"] = sceneToMap(scene)
		agents[i], err = validateSense(obj)
		if err != nil {
			return tickReq{}, err
		}
	}
	var world *worldReq
	switch raw["world"] {
	case nil, false:
	case true:
		parsed, err := validateWorld(map[string]any{"scene": sceneToMap(scene)})
		if err != nil {
			return tickReq{}, err
		}
		world = &parsed
	default:
		obj, err := asObject(raw["world"], "world")
		if err != nil {
			return tickReq{}, err
		}
		obj["scene"] = sceneToMap(scene)
		parsed, err := validateWorld(obj)
		if err != nil {
			return tickReq{}, err
		}
		world = &parsed
	}
	if len(agents) == 0 && world == nil {
		return tickReq{}, errBad(400, "tick 至少要有一个 agent，或把 world 设为 true")
	}
	concurrency := 4
	if raw["concurrency"] != nil {
		n, ok := asFloat(raw["concurrency"])
		if !ok || n != math.Trunc(n) || n < 1 || n > 8 {
			return tickReq{}, errBad(400, "concurrency 必须是 1 到 8 的整数")
		}
		concurrency = int(n)
	}
	return tickReq{Scene: scene, Agents: agents, World: world, Concurrency: concurrency}, nil
}

func validateScene(input any) (scene, error) {
	raw, err := asObject(input, "scene")
	if err != nil {
		return scene{}, err
	}
	place, err := requireText(raw["place"], "scene.place", 200)
	if err != nil {
		return scene{}, err
	}
	s := scene{Place: place}
	if s.TimeOfDay, err = optionalText(raw["timeOfDay"], "scene.timeOfDay", 40); err != nil {
		return scene{}, err
	}
	if s.Weather, err = optionalText(raw["weather"], "scene.weather", 80); err != nil {
		return scene{}, err
	}
	if s.CurrentDirective, err = optionalText(raw["currentDirective"], "scene.currentDirective", 64); err != nil {
		return scene{}, err
	}
	if raw["secondsOnDirective"] != nil {
		n, err := requireNonNegative(raw["secondsOnDirective"], "scene.secondsOnDirective")
		if err != nil {
			return scene{}, err
		}
		s.SecondsOnDirective = &n
	}
	if raw["recentEvents"] != nil {
		s.RecentEvents, err = stringList(raw["recentEvents"], "scene.recentEvents", 6, 180)
		if err != nil {
			return scene{}, err
		}
	}
	return s, nil
}

func validateAgent(input any) (agent, error) {
	raw, err := asObject(input, "agent")
	if err != nil {
		return agent{}, err
	}
	id, err := requireID(raw["id"], "agent.id")
	if err != nil {
		return agent{}, err
	}
	role, err := requireText(raw["role"], "agent.role", 40)
	if err != nil {
		return agent{}, err
	}
	a := agent{ID: id, Role: role}
	for _, item := range []struct {
		key  string
		max  int
		dest *string
	}{
		{"name", 40, &a.Name}, {"personality", 280, &a.Personality}, {"goal", 200, &a.Goal},
		{"faction", 40, &a.Faction}, {"activity", 160, &a.Activity}, {"currentTactic", 64, &a.CurrentTactic},
	} {
		text, err := optionalText(raw[item.key], "agent."+item.key, item.max)
		if err != nil {
			return agent{}, err
		}
		*item.dest = text
	}
	if raw["health"] != nil {
		n, err := requireUnit(raw["health"], "agent.health")
		if err != nil {
			return agent{}, err
		}
		a.Health = &n
	}
	if raw["stamina"] != nil {
		n, err := requireUnit(raw["stamina"], "agent.stamina")
		if err != nil {
			return agent{}, err
		}
		a.Stamina = &n
	}
	if raw["position"] != nil {
		pos, err := validateVec(raw["position"], "agent.position")
		if err != nil {
			return agent{}, err
		}
		a.Pos = &pos
	}
	if raw["currentTargetId"] == nil {
	} else if raw["currentTargetId"] == "" {
		empty := ""
		a.CurrentTargetID = &empty
	} else {
		id, err := requireID(raw["currentTargetId"], "agent.currentTargetId")
		if err != nil {
			return agent{}, err
		}
		a.CurrentTargetID = &id
	}
	if raw["secondsOnTactic"] != nil {
		n, err := requireNonNegative(raw["secondsOnTactic"], "agent.secondsOnTactic")
		if err != nil {
			return agent{}, err
		}
		a.SecondsOnTactic = &n
	}
	if raw["memory"] != nil {
		a.Memory, err = stringList(raw["memory"], "agent.memory", 4, 160)
		if err != nil {
			return agent{}, err
		}
	}
	return a, nil
}

func validateNearby(input any) ([]entity, error) {
	if input == nil {
		return nil, nil
	}
	list, ok := input.([]any)
	if !ok {
		return nil, errBad(400, "nearby 必须是数组")
	}
	if len(list) > 12 {
		return nil, errBad(400, "nearby 最多 12 个，请在游戏侧先筛掉远处的实体")
	}
	out := make([]entity, len(list))
	for i, item := range list {
		parsed, err := validateEntity(item, "nearby["+itoa(i)+"]", "npc")
		if err != nil {
			return nil, err
		}
		out[i] = parsed
	}
	return out, nil
}

func validateEntity(input any, path, fallbackKind string) (entity, error) {
	raw, err := asObject(input, path)
	if err != nil {
		return entity{}, err
	}
	id, err := requireID(raw["id"], path+".id")
	if err != nil {
		return entity{}, err
	}
	kind := fallbackKind
	if raw["kind"] != nil {
		text, ok := raw["kind"].(string)
		if !ok {
			return entity{}, errBad(400, path+".kind 必须是 player、npc、creature、prop、hazard、interest")
		}
		if _, ok := kinds[text]; !ok {
			return entity{}, errBad(400, path+".kind 必须是 player、npc、creature、prop、hazard、interest")
		}
		kind = text
	}
	e := entity{ID: id, Kind: kind}
	if e.Name, err = optionalText(raw["name"], path+".name", 40); err != nil {
		return entity{}, err
	}
	if e.Faction, err = optionalText(raw["faction"], path+".faction", 40); err != nil {
		return entity{}, err
	}
	if e.Activity, err = optionalText(raw["activity"], path+".activity", 160); err != nil {
		return entity{}, err
	}
	if raw["relation"] != nil {
		text, ok := raw["relation"].(string)
		if !ok {
			return entity{}, errBad(400, path+".relation 必须是 ally、enemy、neutral 或 unknown")
		}
		if _, ok := relations[text]; !ok {
			return entity{}, errBad(400, path+".relation 必须是 ally、enemy、neutral 或 unknown")
		}
		e.Relation = text
	}
	if raw["distance"] != nil {
		n, err := requireNonNegative(raw["distance"], path+".distance")
		if err != nil {
			return entity{}, err
		}
		e.Distance = &n
	}
	if raw["health"] != nil {
		n, err := requireUnit(raw["health"], path+".health")
		if err != nil {
			return entity{}, err
		}
		e.Health = &n
	}
	if raw["visible"] != nil {
		b, ok := raw["visible"].(bool)
		if !ok {
			return entity{}, errBad(400, path+".visible 必须是布尔值")
		}
		e.Visible = &b
	}
	if raw["position"] != nil {
		pos, err := validateVec(raw["position"], path+".position")
		if err != nil {
			return entity{}, err
		}
		e.Pos = &pos
	}
	if raw["tags"] != nil {
		e.Tags, err = stringList(raw["tags"], path+".tags", 4, 32)
		if err != nil {
			return entity{}, err
		}
	}
	return e, nil
}

func validatePresences(input any) ([]presence, error) {
	if input == nil {
		return nil, nil
	}
	list, ok := input.([]any)
	if !ok {
		return nil, errBad(400, "presences 必须是数组")
	}
	if len(list) > 16 {
		return nil, errBad(400, "presences 最多 16 个")
	}
	out := make([]presence, len(list))
	for i, item := range list {
		raw, err := asObject(item, "presences["+itoa(i)+"]")
		if err != nil {
			return nil, err
		}
		id, err := requireID(raw["id"], "presences["+itoa(i)+"].id")
		if err != nil {
			return nil, err
		}
		role, err := requireText(raw["role"], "presences["+itoa(i)+"].role", 40)
		if err != nil {
			return nil, err
		}
		p := presence{ID: id, Role: role}
		if p.Activity, err = optionalText(raw["activity"], "presences["+itoa(i)+"].activity", 160); err != nil {
			return nil, err
		}
		if p.Faction, err = optionalText(raw["faction"], "presences["+itoa(i)+"].faction", 40); err != nil {
			return nil, err
		}
		if raw["health"] != nil {
			n, err := requireUnit(raw["health"], "presences["+itoa(i)+"].health")
			if err != nil {
				return nil, err
			}
			p.Health = &n
		}
		out[i] = p
	}
	return out, nil
}

func validateVec(input any, path string) (vec3, error) {
	raw, err := asObject(input, path)
	if err != nil {
		return vec3{}, err
	}
	x, err := requireFinite(raw["x"], path+".x")
	if err != nil {
		return vec3{}, err
	}
	y, err := requireFinite(raw["y"], path+".y")
	if err != nil {
		return vec3{}, err
	}
	v := vec3{X: x, Y: y}
	if raw["z"] != nil {
		z, err := requireFinite(raw["z"], path+".z")
		if err != nil {
			return vec3{}, err
		}
		v.Z = &z
	}
	return v, nil
}

func validateOptionMap(input any, path string, min, max int) (map[string]string, error) {
	raw, err := asObject(input, path)
	if err != nil {
		return nil, err
	}
	if len(raw) < min || len(raw) > max {
		return nil, errBad(400, path+" 需要 "+itoa(min)+" 到 "+itoa(max)+" 个选项")
	}
	out := map[string]string{}
	for key, value := range raw {
		if !keyPattern.MatchString(key) {
			return nil, errBad(400, path+"."+key+" 的 id 需要以字母开头，只能含英文、数字和下划线")
		}
		text, ok := value.(string)
		if !ok || trim(text) == "" {
			return nil, errBad(400, path+"."+key+" 需要一段情境描述，不能为空")
		}
		if utfLen(text) > 500 {
			return nil, errBad(400, path+"."+key+" 的描述超过 500 字")
		}
		out[key] = trim(text)
	}
	return out, nil
}

func asObject(input any, path string) (map[string]any, error) {
	obj, ok := input.(map[string]any)
	if !ok || obj == nil {
		return nil, errBad(400, path+" 必须是对象")
	}
	return obj, nil
}

func requireID(input any, path string) (string, error) {
	text, ok := input.(string)
	if !ok || !idPattern.MatchString(text) {
		return "", errBad(400, path+" 需要 1 到 64 位的英文、数字、_ . : -")
	}
	return text, nil
}

func requireText(input any, path string, max int) (string, error) {
	text, ok := input.(string)
	if !ok || trim(text) == "" {
		return "", errBad(400, path+" 不能为空")
	}
	if utfLen(trim(text)) > max {
		return "", errBad(400, path+" 超过 "+itoa(max)+" 字")
	}
	return trim(text), nil
}

func optionalText(input any, path string, max int) (string, error) {
	if input == nil {
		return "", nil
	}
	return requireText(input, path, max)
}

func requireUnit(input any, path string) (float64, error) {
	value, err := requireFinite(input, path)
	if err != nil {
		return 0, err
	}
	if value < 0 || value > 1 {
		return 0, errBad(400, path+" 必须在 0 到 1 之间")
	}
	return value, nil
}

func requireNonNegative(input any, path string) (float64, error) {
	value, err := requireFinite(input, path)
	if err != nil {
		return 0, err
	}
	if value < 0 {
		return 0, errBad(400, path+" 不能是负数")
	}
	return value, nil
}

func requireFinite(input any, path string) (float64, error) {
	value, ok := asFloat(input)
	if !ok || !finite(value) {
		return 0, errBad(400, path+" 必须是有限数字")
	}
	return value, nil
}

func stringList(input any, path string, max, chars int) ([]string, error) {
	list, ok := input.([]any)
	if !ok {
		return nil, errBad(400, path+" 必须是字符串数组")
	}
	if len(list) > max {
		list = list[len(list)-max:]
	}
	out := make([]string, len(list))
	for i, item := range list {
		text, err := requireText(item, path+"["+itoa(i)+"]", chars)
		if err != nil {
			return nil, err
		}
		out[i] = text
	}
	return out, nil
}

func sceneToMap(s scene) map[string]any {
	out := map[string]any{"place": s.Place}
	putMap(out, "timeOfDay", s.TimeOfDay)
	putMap(out, "weather", s.Weather)
	putMap(out, "currentDirective", s.CurrentDirective)
	if s.SecondsOnDirective != nil {
		out["secondsOnDirective"] = *s.SecondsOnDirective
	}
	if len(s.RecentEvents) > 0 {
		events := make([]any, len(s.RecentEvents))
		for i, event := range s.RecentEvents {
			events[i] = event
		}
		out["recentEvents"] = events
	}
	return out
}

func senseToMap(req senseReq) map[string]any {
	return map[string]any{
		"scene":   sceneToMap(req.Scene),
		"agent":   agentToMap(req.Agent),
		"nearby":  entitiesToAny(req.Nearby),
		"player":  entityToAny(req.Player),
		"tactics": req.Tactics,
		"policy":  policyToMap(req.Policy),
	}
}

func worldToMap(req worldReq) map[string]any {
	presences := make([]any, len(req.Presences))
	for i, item := range req.Presences {
		row := map[string]any{"id": item.ID, "role": item.Role}
		putMap(row, "activity", item.Activity)
		putMap(row, "faction", item.Faction)
		if item.Health != nil {
			row["health"] = *item.Health
		}
		presences[i] = row
	}
	var player any
	if req.Player != nil {
		row := map[string]any{}
		putMap(row, "activity", req.Player.Activity)
		putMap(row, "dominance", req.Player.Dominance)
		if req.Player.Health != nil {
			row["health"] = *req.Player.Health
		}
		player = row
	}
	return map[string]any{
		"scene": sceneToMap(req.Scene), "presences": presences, "player": player,
		"directives": req.Directives, "policy": policyToMap(req.Policy),
	}
}

func agentToMap(a agent) map[string]any {
	out := map[string]any{"id": a.ID, "role": a.Role}
	putMap(out, "name", a.Name)
	putMap(out, "personality", a.Personality)
	putMap(out, "goal", a.Goal)
	putMap(out, "faction", a.Faction)
	putMap(out, "activity", a.Activity)
	putMap(out, "currentTactic", a.CurrentTactic)
	if a.Health != nil {
		out["health"] = *a.Health
	}
	if a.Stamina != nil {
		out["stamina"] = *a.Stamina
	}
	if a.Pos != nil {
		out["position"] = vecToMap(*a.Pos)
	}
	if a.CurrentTargetID != nil {
		out["currentTargetId"] = *a.CurrentTargetID
	}
	if a.SecondsOnTactic != nil {
		out["secondsOnTactic"] = *a.SecondsOnTactic
	}
	if len(a.Memory) > 0 {
		mem := make([]any, len(a.Memory))
		for i, item := range a.Memory {
			mem[i] = item
		}
		out["memory"] = mem
	}
	return out
}

func entitiesToAny(list []entity) []any {
	out := make([]any, len(list))
	for i := range list {
		out[i] = entityToAny(&list[i])
	}
	return out
}

func entityToAny(e *entity) any {
	if e == nil {
		return nil
	}
	out := map[string]any{"id": e.ID, "kind": e.Kind}
	putMap(out, "name", e.Name)
	putMap(out, "faction", e.Faction)
	putMap(out, "relation", e.Relation)
	putMap(out, "activity", e.Activity)
	if e.Distance != nil {
		out["distance"] = *e.Distance
	}
	if e.Health != nil {
		out["health"] = *e.Health
	}
	if e.Visible != nil {
		out["visible"] = *e.Visible
	}
	if e.Pos != nil {
		out["position"] = vecToMap(*e.Pos)
	}
	if len(e.Tags) > 0 {
		tags := make([]any, len(e.Tags))
		for i, tag := range e.Tags {
			tags[i] = tag
		}
		out["tags"] = tags
	}
	return out
}

func vecToMap(v vec3) map[string]any {
	out := map[string]any{"x": v.X, "y": v.Y}
	if v.Z != nil {
		out["z"] = *v.Z
	}
	return out
}

func policyToMap(p Policy) map[string]any {
	return map[string]any{
		"switchConfidence": p.SwitchConfidence,
		"interruptAt":      p.InterruptAt,
		"uncertainMargin":  p.UncertainMargin,
	}
}

func utfLen(s string) int { return len([]rune(s)) }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func trimFloat(n float64) string {
	s := strings.TrimRight(strings.TrimRight(sprintf(n), "0"), ".")
	if s == "" || s == "-" {
		return "0"
	}
	return s
}

func sprintf(n float64) string {
	return strings.TrimSpace(strings.ReplaceAll(jsonNumberString(n), ",", ""))
}

func jsonNumberString(n float64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
