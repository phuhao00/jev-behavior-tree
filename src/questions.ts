import { THREAT_LEVELS, TENSION_LEVELS, openingQuestion } from './roles'
import type { RosterEntry } from './types'

export function buildAgentQuestions(args: {
  tactics: Record<string, string>
  roster: RosterEntry[]
  role: string
  askPlayer: boolean
  askAlly: boolean
}): Record<string, unknown> {
  const questions: Record<string, unknown> = {
    tactic: {
      type: 'choice',
      instructions:
        'Which single tactic should this agent commit to now? Use `agent.role`, `agent.personality`, `agent.goal`, `agent.health`, `agent.currentTactic`, `agent.activity`, `scene`, `player`, and `nearby`. Choose hold when the current tactic still matches the moment. Do not invent a tactic.',
      criteria: args.tactics,
    },
    threat: {
      type: 'score',
      instructions:
        'How much danger is this agent in right now? Match `agent.health`, `player`, and `nearby` to a situation. Score this agent, not the whole scene.',
      criteria: [...THREAT_LEVELS],
    },
    interrupt: {
      type: 'boolean',
      instructions:
        'Should this agent abort `agent.currentTactic` immediately? Use `agent.secondsOnTactic`, `agent.health`, `player`, and `nearby`.',
      criteria: {
        true: 'The assumption behind the current tactic just broke: a new threat, a dying ally, or the target is gone.',
        false: 'The current tactic still fits, or there is no current tactic that needs aborting.',
      },
    },
    opening: {
      type: 'boolean',
      ...openingQuestion(args.role),
    },
  }

  if (args.roster.length > 0) {
    questions.target = {
      type: 'choice',
      instructions:
        'Which entity is the focus of this moment? Choose none when the agent should not lock onto anyone. The option id matches `id` on `player` or `nearby`.',
      criteria: targetCriteria(args.roster),
    }
  }

  if (args.askPlayer) {
    questions.playerHostile = {
      type: 'boolean',
      instructions:
        'Is `player` about to attack this agent or an ally, as opposed to passing by, talking, or leaving?',
      criteria: {
        true: 'A weapon is out, they are sprinting in, they just struck, or they are clearly hunting.',
        false: 'Sheathed, idle, talking, leaving, or moving past without a threat.',
      },
    }
  }

  if (args.askAlly) {
    questions.allyNeedsHelp = {
      type: 'boolean',
      instructions: 'Does an ally in `nearby` need this agent\'s help within the next few seconds?',
      criteria: {
        true: 'An ally is hurt, falling, cornered, or calling for help.',
        false: 'Allies are fine, or none of them need this agent.',
      },
    }
  }

  return questions
}

export function buildWorldQuestions(args: {
  directives: Record<string, string>
  askPlayer: boolean
}): Record<string, unknown> {
  const questions: Record<string, unknown> = {
    directive: {
      type: 'choice',
      instructions:
        'Which single beat should this place play now? Use `scene.currentDirective`, `scene.recentEvents`, `player`, and `presences`. Choose hold_atmosphere when the current beat still fits. Do not invent a beat.',
      criteria: args.directives,
    },
    tension: {
      type: 'score',
      instructions:
        'Where does the place sit right now, as a situation rather than a vague intensity? Use `scene`, `player`, and `presences`.',
      criteria: [...TENSION_LEVELS],
    },
  }

  if (args.askPlayer) {
    questions.overextended = {
      type: 'boolean',
      instructions:
        'Is the player overextended: too deep, too hurt, or too committed, so the place could punish them?',
      criteria: {
        true: 'The player is deep in, badly hurt, surrounded, or cut off from an easy step back.',
        false: 'The player has room, health, or an obvious way to step back.',
      },
    }
  }

  return questions
}

function targetCriteria(roster: RosterEntry[]): Record<string, string> {
  const criteria: Record<string, string> = {
    none: 'No specific entity. The moment is about the place or the self, not a lock-on.',
  }
  for (const entry of roster) criteria[entry.choiceKey] = describeEntry(entry)
  return criteria
}

function describeEntry(entry: RosterEntry): string {
  const parts = [
    entry.name !== entry.id ? `${entry.name} (${entry.id})` : entry.id,
    entry.kind,
    entry.relation,
    entry.distance != null ? `${entry.distance}m` : 'distance unknown',
    entry.visible === true ? 'visible' : entry.visible === false ? 'not visible' : 'visibility unknown',
    entry.health != null ? `health ${entry.health}` : null,
    entry.activity || null,
    entry.tags.length > 0 ? `tags ${entry.tags.join(', ')}` : null,
  ]
  return parts.filter(Boolean).join(', ')
}
