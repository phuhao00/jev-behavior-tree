package main

import "math"

type Tri int

const (
	TriFalse Tri = iota
	TriTrue
	TriUncertain
)

func (t Tri) MarshalJSON() ([]byte, error) {
	switch t {
	case TriTrue:
		return []byte("true"), nil
	case TriFalse:
		return []byte("false"), nil
	default:
		return []byte(`"uncertain"`), nil
	}
}

type Policy struct {
	SwitchConfidence float64
	InterruptAt      float64
	UncertainMargin  float64
}

var defaultPolicy = Policy{SwitchConfidence: 0.72, InterruptAt: 0.75, UncertainMargin: 0.12}

type Decision struct {
	Disposition string
	Executing   string
	Because     string
}

type TargetCandidate struct {
	ID       string
	Relation string
	Distance *float64
	Health   *float64
	Kind     string
}

type Bound struct {
	TargetID     *string
	TargetSource string
}

func resolvePolicy(in map[string]any) (Policy, error) {
	policy := defaultPolicy
	if in == nil {
		return policy, nil
	}
	for _, key := range []string{"switchConfidence", "interruptAt", "uncertainMargin"} {
		raw, ok := in[key]
		if !ok || raw == nil {
			continue
		}
		value, ok := asFloat(raw)
		if !ok || math.IsNaN(value) || value < 0 || value > 1 {
			return Policy{}, errBad(400, "policy."+key+" 必须是 0 到 1 之间的数字")
		}
		switch key {
		case "switchConfidence":
			policy.SwitchConfidence = value
		case "interruptAt":
			policy.InterruptAt = value
		case "uncertainMargin":
			policy.UncertainMargin = value
		}
	}
	return policy, nil
}

func classifyBoolean(probability *float64, margin float64) Tri {
	if probability == nil || math.IsNaN(*probability) {
		return TriUncertain
	}
	if *probability >= 0.5+margin {
		return TriTrue
	}
	if *probability <= 0.5-margin {
		return TriFalse
	}
	return TriUncertain
}

func classifyInterrupt(probability *float64, interruptAt float64) Tri {
	if probability == nil || math.IsNaN(*probability) {
		return TriUncertain
	}
	if *probability >= interruptAt {
		return TriTrue
	}
	if *probability <= 1-interruptAt {
		return TriFalse
	}
	return TriUncertain
}

func decideDisposition(current, suggested string, confidence *float64, interrupt Tri, holdKey *string, switchConfidence float64) Decision {
	current = trim(current)
	expanded := suggested
	if holdKey != nil && suggested == *holdKey && current != "" {
		expanded = current
	}
	if current == "" {
		return Decision{Disposition: "switch", Executing: expanded, Because: "first-decision"}
	}
	if expanded == current {
		return Decision{Disposition: "continue", Executing: current, Because: "still-fitting"}
	}
	if interrupt == TriTrue {
		return Decision{Disposition: "switch", Executing: expanded, Because: "interrupt"}
	}
	if confidence == nil || *confidence >= switchConfidence {
		return Decision{Disposition: "switch", Executing: expanded, Because: "confident"}
	}
	return Decision{Disposition: "hold", Executing: current, Because: "hysteresis"}
}

func tacticNeedsTarget(tactic string) bool {
	switch tactic {
	case "hold", "patrol", "hide", "flee":
		return false
	default:
		return true
	}
}

func bindTarget(tactic, modelTargetID string, roster []TargetCandidate) Bound {
	if !tacticNeedsTarget(tactic) {
		return Bound{TargetSource: "none"}
	}
	if modelTargetID != "none" && modelTargetID != "" {
		for _, entry := range roster {
			if entry.ID == modelTargetID {
				id := modelTargetID
				return Bound{TargetID: &id, TargetSource: "model"}
			}
		}
	}
	if fallback := fallbackTarget(tactic, roster); fallback != "" {
		id := fallback
		return Bound{TargetID: &id, TargetSource: "geometric-fallback"}
	}
	return Bound{TargetSource: "none"}
}

func scoreBand(score float64, labels []string) ScoreRead {
	max := 0
	if n := len(labels) - 1; n > 0 {
		max = n
	}
	clamped := math.Min(float64(max), math.Max(0, score))
	level := int(math.Round(clamped))
	if level < 0 {
		level = 0
	}
	if level > max {
		level = max
	}
	label := "unknown"
	if level < len(labels) {
		label = labels[level]
	}
	return ScoreRead{Score: math.Round(clamped*100) / 100, Level: level, Label: label}
}

func fallbackTarget(tactic string, roster []TargetCandidate) string {
	ranked := append([]TargetCandidate(nil), roster...)
	sortByDistance(ranked)
	if tactic == "assist" {
		var allies []TargetCandidate
		for _, entry := range ranked {
			if entry.Relation == "ally" {
				allies = append(allies, entry)
			}
		}
		sortAllies(allies)
		if len(allies) > 0 {
			return allies[0].ID
		}
		return ""
	}
	if tactic == "investigate" || tactic == "interact" {
		for _, entry := range ranked {
			if entry.Kind == "interest" || entry.Kind == "hazard" || entry.Kind == "prop" {
				return entry.ID
			}
		}
		if len(ranked) > 0 {
			return ranked[0].ID
		}
		return ""
	}
	for _, entry := range ranked {
		if entry.Relation == "enemy" {
			return entry.ID
		}
	}
	if len(ranked) > 0 {
		return ranked[0].ID
	}
	return ""
}

func sortByDistance(items []TargetCandidate) {
	for i := 1; i < len(items); i++ {
		j := i
		for j > 0 && distOrFar(items[j]) < distOrFar(items[j-1]) {
			items[j], items[j-1] = items[j-1], items[j]
			j--
		}
	}
}

func sortAllies(items []TargetCandidate) {
	for i := 1; i < len(items); i++ {
		j := i
		for j > 0 && allyLess(items[j], items[j-1]) {
			items[j], items[j-1] = items[j-1], items[j]
			j--
		}
	}
}

func allyLess(a, b TargetCandidate) bool {
	ha, hb := 1.0, 1.0
	if a.Health != nil {
		ha = *a.Health
	}
	if b.Health != nil {
		hb = *b.Health
	}
	if ha != hb {
		return ha < hb
	}
	return distOrFar(a) < distOrFar(b)
}

func distOrFar(item TargetCandidate) float64 {
	if item.Distance == nil {
		return 1e9
	}
	return *item.Distance
}
