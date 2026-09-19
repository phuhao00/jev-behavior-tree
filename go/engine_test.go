package main

import "testing"

func guardBody(health float64, tactic, target string) map[string]any {
	agent := map[string]any{
		"id":              "rook",
		"role":            "guard",
		"health":          health,
		"currentTactic":   tactic,
		"secondsOnTactic": 4,
	}
	if target != "" {
		agent["currentTargetId"] = target
	}
	return map[string]any{
		"scene": map[string]any{"place": "test yard"},
		"agent": agent,
		"player": map[string]any{
			"id": "player", "kind": "player", "distance": 4, "visible": true,
			"health": 1, "relation": "neutral", "activity": "sprinting with a blade",
		},
		"nearby": []any{
			map[string]any{"id": "wolf", "kind": "creature", "distance": 9, "health": 0.8, "relation": "enemy", "activity": "circling"},
			map[string]any{"id": "mira", "kind": "npc", "faction": "chapel", "distance": 3, "health": 0.2, "relation": "ally", "activity": "on the ground"},
		},
	}
}

func answered(tactic string, confidence float64, interrupt float64, target string) Judge {
	return func(any, map[string]any) (judgeResult, error) {
		return judgeResult{
			Answers: map[string]any{
				"tactic":        map[string]any{"type": "choice", "choice": tactic, "probabilities": map[string]any{tactic: 0.8, "hold": 0.2}},
				"target":        map[string]any{"type": "choice", "choice": target, "probabilities": map[string]any{target: 0.9, "none": 0.1}},
				"threat":        map[string]any{"type": "score", "score": 2.2},
				"interrupt":     map[string]any{"type": "boolean", "probability": interrupt},
				"opening":       map[string]any{"type": "boolean", "probability": 0.2},
				"playerHostile": map[string]any{"type": "boolean", "probability": 0.91},
				"allyNeedsHelp": map[string]any{"type": "boolean", "probability": 0.2},
			},
			ProviderMetadata: map[string]any{"typesafe": map[string]any{"confidence": map[string]any{"tactic": confidence, "threat": 0.8}}},
			ModelID:          "typesafe-ai/jev",
		}, nil
	}
}

func TestDeadAgentSkipsJudge(t *testing.T) {
	called := false
	impulse, err := senseAgent(guardBody(0, "patrol", "wolf"), func(any, map[string]any) (judgeResult, error) {
		called = true
		return judgeResult{}, errBad(500, "should not be called")
	})
	if err != nil {
		t.Fatal(err)
	}
	if called || impulse.Disposition != "incapacitated" || impulse.Tactic != "none" {
		t.Fatalf("called=%v impulse=%+v", called, impulse)
	}
}

func TestLowConfidenceHoldsPatrol(t *testing.T) {
	impulse, err := senseAgent(guardBody(1, "patrol", "wolf"), answered("engage", 0.2, 0.1, "player"))
	if err != nil {
		t.Fatal(err)
	}
	if impulse.SuggestedTactic != "engage" || impulse.Tactic != "patrol" || impulse.Disposition != "hold" || impulse.Because != "hysteresis" {
		t.Fatalf("%+v", impulse)
	}
	if impulse.TargetID != nil || impulse.PlayerHostile != TriTrue || impulse.ConfidenceSource != "typesafe" {
		t.Fatalf("%+v", impulse)
	}
}

func TestHighConfidenceLocksModelTarget(t *testing.T) {
	impulse, err := senseAgent(guardBody(1, "patrol", ""), answered("engage", 0.9, 0.1, "player"))
	if err != nil {
		t.Fatal(err)
	}
	if impulse.Tactic != "engage" || impulse.Because != "confident" || impulse.TargetSource != "model" || impulse.TargetID == nil || *impulse.TargetID != "player" {
		t.Fatalf("%+v", impulse)
	}
}

func TestContinueKeepsCurrentTarget(t *testing.T) {
	impulse, err := senseAgent(guardBody(1, "engage", "wolf"), answered("engage", 0.95, 0.1, "player"))
	if err != nil {
		t.Fatal(err)
	}
	if impulse.Disposition != "continue" || impulse.TargetSource != "kept" || impulse.TargetID == nil || *impulse.TargetID != "wolf" {
		t.Fatalf("%+v", impulse)
	}
	if impulse.SuggestedTargetID == nil || *impulse.SuggestedTargetID != "player" {
		t.Fatalf("%+v", impulse)
	}
}

func TestWorldHoldsAtmosphere(t *testing.T) {
	world, err := senseWorld(map[string]any{
		"scene":  map[string]any{"place": "chapel", "currentDirective": "hold_atmosphere", "secondsOnDirective": 10},
		"player": map[string]any{"activity": "walking", "health": 1, "dominance": "passing"},
	}, func(any, map[string]any) (judgeResult, error) {
		return judgeResult{
			Answers: map[string]any{
				"directive":    map[string]any{"type": "choice", "choice": "ambush_now", "probabilities": map[string]any{"ambush_now": 0.55, "hold_atmosphere": 0.45}},
				"tension":      map[string]any{"type": "score", "score": 1.1},
				"overextended": map[string]any{"type": "boolean", "probability": 0.2},
			},
			ProviderMetadata: map[string]any{"typesafe": map[string]any{"confidence": map[string]any{"directive": 0.3}}},
			ModelID:          "typesafe-ai/jev",
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if world.Directive != "hold_atmosphere" || world.SuggestedDirective != "ambush_now" || world.Disposition != "hold" || world.PlayerOverextended != TriFalse {
		t.Fatalf("%+v", world)
	}
}
