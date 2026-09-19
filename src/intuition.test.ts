import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import type { Judge } from './jev'
import { senseAgent, senseWorld } from './intuition'
import { bindTarget, decideDisposition } from './policy'

const guard = {
  scene: { place: 'test yard' },
  agent: {
    id: 'rook',
    role: 'guard',
    health: 1,
    currentTactic: 'patrol',
    currentTargetId: 'wolf',
    secondsOnTactic: 4,
  },
  player: {
    id: 'player',
    kind: 'player' as const,
    distance: 4,
    visible: true,
    health: 1,
    relation: 'neutral' as const,
    activity: 'sprinting with a blade',
  },
  nearby: [
    {
      id: 'wolf',
      kind: 'creature' as const,
      distance: 9,
      health: 0.8,
      relation: 'enemy' as const,
      activity: 'circling',
    },
    {
      id: 'mira',
      kind: 'npc' as const,
      faction: 'chapel',
      distance: 3,
      health: 0.2,
      relation: 'ally' as const,
      activity: 'on the ground',
    },
  ],
}

function answered(tactic: string, confidence: number, interrupt = 0.1, target = 'player'): Judge {
  return async () => ({
    answers: {
      tactic: {
        type: 'choice',
        choice: tactic,
        probabilities: { [tactic]: 0.8, hold: 0.2 },
      },
      target: {
        type: 'choice',
        choice: target,
        probabilities: { [target]: 0.9, none: 0.1 },
      },
      threat: { type: 'score', score: 2.2 },
      interrupt: { type: 'boolean', probability: interrupt },
      opening: { type: 'boolean', probability: 0.2 },
      playerHostile: { type: 'boolean', probability: 0.91 },
      allyNeedsHelp: { type: 'boolean', probability: 0.2 },
    },
    providerMetadata: { typesafe: { confidence: { tactic: confidence, threat: 0.8 } } },
    response: { modelId: 'typesafe-ai/jev' },
    usage: { inputTokens: 20, outputTokens: 0 },
  })
}

describe('disposition', () => {
  it('keeps the current tactic when the new read is not confident', () => {
    const decision = decideDisposition({
      current: 'patrol',
      suggested: 'engage',
      confidence: 0.4,
      interrupt: false,
      holdKey: 'hold',
      switchConfidence: 0.72,
    })
    assert.equal(decision.disposition, 'hold')
    assert.equal(decision.executing, 'patrol')
    assert.equal(decision.because, 'hysteresis')
  })

  it('lets a hard interrupt through even when confidence is low', () => {
    const decision = decideDisposition({
      current: 'patrol',
      suggested: 'flee',
      confidence: 0.4,
      interrupt: true,
      holdKey: 'hold',
      switchConfidence: 0.72,
    })
    assert.equal(decision.executing, 'flee')
    assert.equal(decision.because, 'interrupt')
  })

  it('expands hold back into the tactic already running', () => {
    const decision = decideDisposition({
      current: 'patrol',
      suggested: 'hold',
      confidence: 0.95,
      interrupt: true,
      holdKey: 'hold',
      switchConfidence: 0.72,
    })
    assert.equal(decision.executing, 'patrol')
    assert.equal(decision.because, 'still-fitting')
  })
})

describe('targets', () => {
  it('does not lock a target for patrol, flee, or hide', () => {
    assert.equal(bindTarget('patrol', 'player', []).targetSource, 'none')
  })

  it('assists the weakest ally rather than the nearest enemy', () => {
    const bound = bindTarget('assist', 'none', [
      { id: 'wolf', relation: 'enemy', distance: 2, health: 1, kind: 'creature' },
      { id: 'mira', relation: 'ally', distance: 6, health: 0.2, kind: 'npc' },
      { id: 'squire', relation: 'ally', distance: 3, health: 0.9, kind: 'npc' },
    ])
    assert.equal(bound.targetId, 'mira')
    assert.equal(bound.targetSource, 'geometric-fallback')
  })
})

describe('senseAgent', () => {
  it('does not call Jev for a dead agent', async () => {
    let called = false
    const impulse = await senseAgent(
      { ...guard, agent: { ...guard.agent, health: 0 } },
      async () => {
        called = true
        throw new Error('should not be called')
      },
    )
    assert.equal(called, false)
    assert.equal(impulse.disposition, 'incapacitated')
    assert.equal(impulse.tactic, 'none')
  })

  it('holds the running tactic when confidence is low', async () => {
    const impulse = await senseAgent(guard, answered('engage', 0.2))
    assert.equal(impulse.suggestedTactic, 'engage')
    assert.equal(impulse.tactic, 'patrol')
    assert.equal(impulse.disposition, 'hold')
    assert.equal(impulse.because, 'hysteresis')
    assert.equal(impulse.targetId, null)
    assert.equal(impulse.playerHostile, true)
    assert.equal(impulse.confidenceSource, 'typesafe')
  })

  it('switches and locks the model target when confidence is high', async () => {
    const impulse = await senseAgent(
      { ...guard, agent: { ...guard.agent, currentTargetId: null } },
      answered('engage', 0.9, 0.1, 'player'),
    )
    assert.equal(impulse.tactic, 'engage')
    assert.equal(impulse.because, 'confident')
    assert.equal(impulse.targetId, 'player')
    assert.equal(impulse.targetSource, 'model')
  })

  it('keeps the current target while the same tactic continues', async () => {
    const impulse = await senseAgent(
      { ...guard, agent: { ...guard.agent, currentTactic: 'engage', currentTargetId: 'wolf' } },
      answered('engage', 0.95, 0.1, 'player'),
    )
    assert.equal(impulse.disposition, 'continue')
    assert.equal(impulse.targetId, 'wolf')
    assert.equal(impulse.targetSource, 'kept')
    assert.equal(impulse.suggestedTargetId, 'player')
  })
})

describe('senseWorld', () => {
  it('does not flip the atmosphere on a low-confidence beat', async () => {
    const world = await senseWorld(
      {
        scene: {
          place: 'chapel',
          currentDirective: 'hold_atmosphere',
          secondsOnDirective: 10,
        },
        player: { activity: 'walking', health: 1, dominance: 'passing' },
      },
      async () => ({
        answers: {
          directive: {
            type: 'choice',
            choice: 'ambush_now',
            probabilities: { ambush_now: 0.55, hold_atmosphere: 0.45 },
          },
          tension: { type: 'score', score: 1.1 },
          overextended: { type: 'boolean', probability: 0.2 },
        },
        providerMetadata: { typesafe: { confidence: { directive: 0.3 } } },
        response: { modelId: 'typesafe-ai/jev' },
      }),
    )
    assert.equal(world.directive, 'hold_atmosphere')
    assert.equal(world.suggestedDirective, 'ambush_now')
    assert.equal(world.disposition, 'hold')
    assert.equal(world.playerOverextended, false)
  })
})
