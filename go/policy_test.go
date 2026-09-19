package main

import "testing"

func TestLowConfidenceHolds(t *testing.T) {
	conf := 0.4
	hold := "hold"
	got := decideDisposition("patrol", "engage", &conf, TriFalse, &hold, 0.72)
	if got.Disposition != "hold" || got.Executing != "patrol" || got.Because != "hysteresis" {
		t.Fatalf("%+v", got)
	}
}

func TestInterruptSwitches(t *testing.T) {
	conf := 0.4
	hold := "hold"
	got := decideDisposition("patrol", "flee", &conf, TriTrue, &hold, 0.72)
	if got.Executing != "flee" || got.Because != "interrupt" {
		t.Fatalf("%+v", got)
	}
}

func TestHoldExpands(t *testing.T) {
	conf := 0.95
	hold := "hold"
	got := decideDisposition("patrol", "hold", &conf, TriTrue, &hold, 0.72)
	if got.Executing != "patrol" || got.Because != "still-fitting" {
		t.Fatalf("%+v", got)
	}
}

func TestBindSkipsPatrol(t *testing.T) {
	if bindTarget("patrol", "player", nil).TargetSource != "none" {
		t.Fatal("patrol should not lock a target")
	}
}

func TestAssistWeakestAlly(t *testing.T) {
	h1, h02, h09 := 1.0, 0.2, 0.9
	d2, d6, d3 := 2.0, 6.0, 3.0
	got := bindTarget("assist", "none", []TargetCandidate{
		{ID: "wolf", Relation: "enemy", Distance: &d2, Health: &h1, Kind: "creature"},
		{ID: "mira", Relation: "ally", Distance: &d6, Health: &h02, Kind: "npc"},
		{ID: "squire", Relation: "ally", Distance: &d3, Health: &h09, Kind: "npc"},
	})
	if got.TargetID == nil || *got.TargetID != "mira" || got.TargetSource != "geometric-fallback" {
		t.Fatalf("%+v", got)
	}
}
