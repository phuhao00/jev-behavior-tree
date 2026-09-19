package main

const (
	holdTactic    = "hold"
	holdDirective = "hold_atmosphere"
)

func threatLevels() []string {
	return []string{
		"Calm. No one the agent can perceive is about to hurt them in the next few seconds.",
		"Watchful. Something could turn bad, but there is still time to look, speak, or step aside.",
		"Pressed. A bad outcome is likely within a few seconds unless the agent changes what it is doing.",
		"Breaking. This agent is already being hurt, cornered, or about to fail its goal.",
	}
}

func tensionLevels() []string {
	return []string{
		"Lull. The place can breathe. Adding a threat, closing a gate, or darkening the weather would be forcing it.",
		"Watchful. The scene is awake. Existing inhabitants should pay attention, but a new ambush is not earned yet.",
		"Strained. Sightlines, exits, or attention should tighten. Someone is pushing, or something here is about to break.",
		"Breaking. A spike is already true. Fight, flight, or collapse is happening, and a calm beat would feel like a bug.",
	}
}

func worldDirectives() map[string]string {
	return map[string]string{
		"hold_atmosphere": "Keep the current weather, gates, and spawn pressure. The scene does not need a new beat.",
		"tighten_patrol":  "Existing threats pay more attention and move with purpose. Do not add an ambush and do not close the exits.",
		"fog_stalk":       "Shorten sightlines. Threats already here should stalk rather than charge. No fresh spawn is required.",
		"ambush_now":      "A concealed threat should reveal itself now. The player is exposed, distracted, or overextended, and another lull would feel empty.",
		"release":         "Pull the danger back. Open a path, clear the air, let the player recover. Use this after a spike, not in the middle of one.",
		"seal_escape":     "Close a gate or collapse the easy way out. Use it when the player is about to leave a scene that is not finished.",
	}
}

func tacticsForRole(role string) map[string]string {
	switch role {
	case "guard":
		return map[string]string{
			"hold":        "Stay on the current action. The post, the watch, or the fight has not actually changed.",
			"patrol":      "Walk the post. Nobody here is a problem yet, and this agent is supposed to be seen.",
			"investigate": "Something is off but not yet a fight: a noise, a figure too far to judge, a door that should be shut. Go look, weapon ready, without charging.",
			"challenge":   "A person is close enough to answer. Tell them to stop or state their business. Do not strike yet.",
			"engage":      "A real attack is happening, or someone is forcing the post right now. Commit to stopping them.",
			"flee":        "The post is already lost, or this agent is about to die. Leave. Do not pick this while health is sound and the post still stands.",
			"hide":        "Break line of sight because fighting now is suicide. Rare for a posted guard: only when overwhelmed.",
			"assist":      "An ally is losing a fight or is down nearby. Leave the exact spot to keep that ally alive.",
			"interact":    "Use the place: bar a door, ring an alarm, pick up a dropped weapon. Not a conversation and not a strike.",
		}
	case "civilian":
		return map[string]string{
			"hold":        "Keep doing the current thing, including staying hidden. Moving would be louder or worse than staying.",
			"patrol":      "Walk an ordinary path. Only when the place still feels normal. A civilian does not patrol a battlefield.",
			"investigate": "Look only if nothing here looks dangerous. A noise during a fight is not this.",
			"challenge":   "Words from a safe distance: a plea or a warning. Not a threat display, and not if this agent is the target.",
			"engage":      "Last resort. Escape is gone, this agent is cornered, and staying still means dying.",
			"flee":        "Get away from the danger toward open space or an exit. The default once a threat becomes real.",
			"hide":        "Get out of sight and go still. Better than running when the threat is close and looking.",
			"assist":      "Help only if it does not clearly get this agent killed. A step of cover counts. Joining a melee does not.",
			"interact":    "A door, a latch, a crowd to disappear into. Use the place to get safer.",
		}
	case "predator":
		return map[string]string{
			"hold":        "Stay in the stalk. The prey has not given a better opening, and breaking the stalk would waste it.",
			"patrol":      "Range the area looking for prey. Nothing worth hunting is in front of this agent.",
			"investigate": "A scent, a sound, a shape. Close the distance without committing the attack.",
			"challenge":   "A threat display, not a conversation: circle, snarl, cut off a line. Use it when a direct lunge would miss.",
			"engage":      "The prey is close, exposed, or already bleeding. Commit to the attack.",
			"flee":        "Wounded, outnumbered, or the prey is fighting back too well. Break off. Predators leave.",
			"hide":        "Drop into cover and freeze because the prey is looking this way.",
			"assist":      "Another of the same pack is already in the fight, and the kill is more sure together.",
			"interact":    "Feed, drag a kill, or nose a carcass. The fight is over, or it has not started.",
		}
	case "companion":
		return map[string]string{
			"hold":        "The current follow, fight, or watch still matches what the player needs.",
			"patrol":      "Scout a short arc around the player when nothing is wrong. Do not wander off.",
			"investigate": "Check a threat the player has not seen yet, then come back.",
			"challenge":   "Step between the player and someone who has not struck yet. Warn them off.",
			"engage":      "Someone is attacking the player or this companion. Stop them.",
			"flee":        "The player is leaving a lost fight, or this companion is about to die and cannot protect anyone by staying.",
			"hide":        "Drop out of sight only to set up the next assist. Not to abandon the player.",
			"assist":      "The player or an ally is hurt or losing. Get to them: block, drag, or draw the hit.",
			"interact":    "Open the path the player needs: a door, a lever, a heal. Not small talk.",
		}
	case "ambient":
		return map[string]string{
			"hold":        "The current loop still belongs. Fire, wind, crowd, or idle creature has no reason to change.",
			"patrol":      "Continue a slow drift. The scene is alive but not reacting to anyone.",
			"investigate": "Lean toward a new stimulus: a turn, a flicker, a murmur. Do not cross the scene.",
			"challenge":   "A sharp warning that is not an attack: birds lift, fire pops, a rope creaks.",
			"engage":      "The place itself touches someone: a branch falls, steam vents, the crowd shoves. Only when they are already on top of it.",
			"flee":        "Clear out. Birds leave, vermin scatter, smoke thins and runs.",
			"hide":        "Go quiet and dark. The place pretends to be empty.",
			"assist":      "Part the way or soften for an ally: a lantern brightens, the crowd opens a gap.",
			"interact":    "A prop finishes a small action: a shutter slams, a bell answers, a puddle ripples.",
		}
	default:
		return nil
	}
}

func priorForRole(role string) (personality, goal string, ok bool) {
	switch role {
	case "guard":
		return "Posted to this spot. Leaves the post only for a real threat or a fallen ally.", "Keep this place from being taken.", true
	case "civilian":
		return "Wants to live through the next minute. Will not play the hero.", "Survive. Run or hide before fighting.", true
	case "predator":
		return "Hunts. Prefers a sure kill over a fair fight, and leaves when wounded.", "Feed, then leave.", true
	case "companion":
		return "Stays with the player. Will step into a hit for them, and will not abandon a fallen ally.", "Keep the player alive.", true
	case "ambient":
		return "A piece of the place. It reacts, then settles. It does not chase.", "Belong to the scene.", true
	default:
		return "", "", false
	}
}

func openingKindFor(role string) string {
	switch role {
	case "civilian", "ambient":
		return "escape"
	case "companion":
		return "scout"
	case "guard", "predator":
		return "ambush"
	default:
		return "generic"
	}
}

func openingQuestion(role string) (string, map[string]string) {
	switch role {
	case "civilian", "ambient":
		return "Is this a good moment for this agent to slip away or go still without being noticed? Use `scene`, `player`, and `nearby`.", map[string]string{
			"true":  "Attention is elsewhere, cover exists, or the threat is busy with someone else.",
			"false": "This agent is already seen, cornered, or nobody is pursuing them.",
		}
	case "companion":
		return "Is this a good moment to scout, intercept, or step in for the player, rather than keep trailing? Use `player` and `nearby`.", map[string]string{
			"true":  "A threat is forming that the player has not answered, or an ally is about to drop.",
			"false": "Staying on the current follow or fight is the useful thing.",
		}
	case "guard", "predator":
		return "Is this a good moment to strike or confront first, rather than wait? Use cover, surprise, `player` activity, and `nearby`.", map[string]string{
			"true":  "The target is exposed, distracted, or already committing, and waiting would waste it.",
			"false": "The target is ready, too far, or this agent would be striking into a prepared defense.",
		}
	default:
		return "Is there a fleeting opening this agent should take right now, given `agent.goal`?", map[string]string{
			"true":  "Waiting would waste a real opening.",
			"false": "Nothing is opening, or acting now is worse than waiting.",
		}
	}
}
