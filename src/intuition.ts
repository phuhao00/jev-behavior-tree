import { compactAgent, compactWorld } from './compact'
import { errorMessage, errorStatus, IntuitionError } from './errors'
import { judge, modelId, type Judge, type JudgeResult } from './jev'
import { asRecord, readChoice, readModelId, readProbability, readScore, readUsage, resolveConfidence } from './parse'
import {
  bindTarget,
  classifyBoolean,
  classifyInterrupt,
  decideDisposition,
  scoreBand,
  tacticNeedsTarget,
} from './policy'
import { buildAgentQuestions, buildWorldQuestions } from './questions'
import { HOLD_DIRECTIVE, HOLD_TACTIC, TENSION_LEVELS, THREAT_LEVELS, openingKindFor } from './roles'
import type {
  AgentTickError,
  Impulse,
  RosterEntry,
  TargetSource,
  TickResult,
  WorldImpulse,
} from './types'
import { validateSense, validateTick, validateWorld, type NormalizedSense, type NormalizedWorld } from './validate'

export async function senseAgent(input: unknown, judgeFn: Judge = judge): Promise<Impulse> {
  const started = Date.now()
  const req = validateSense(input)
  if (typeof req.agent.health === 'number' && req.agent.health <= 0) {
    return incapacitated(req, started)
  }

  const compact = compactAgent(req)
  const askAlly = compact.roster.some((entry) => entry.relation === 'ally')
  const questions = buildAgentQuestions({
    tactics: req.tactics,
    roster: compact.roster,
    role: req.agent.role,
    askPlayer: compact.hasPlayer,
    askAlly,
  })
  const result = await judgeFn(compact.state, questions)
  return composeAgent({ req, compact, result, started, askAlly })
}

export async function senseWorld(input: unknown, judgeFn: Judge = judge): Promise<WorldImpulse> {
  const started = Date.now()
  const req = validateWorld(input)
  const state = compactWorld(req)
  const questions = buildWorldQuestions({
    directives: req.directives,
    askPlayer: req.player != null,
  })
  const result = await judgeFn(state, questions)
  return composeWorld({ req, result, started })
}

export async function senseTick(input: unknown, judgeFn: Judge = judge): Promise<TickResult> {
  const started = Date.now()
  const req = validateTick(input)
  const agents: Array<Impulse | AgentTickError> = new Array(req.agents.length)
  let world: TickResult['world'] = null
  const jobs: Array<() => Promise<void>> = []

  if (req.world) {
    const worldRequest = req.world
    jobs.push(async () => {
      try {
        world = await senseWorld(worldRequest, judgeFn)
      } catch (err) {
        world = { error: errorMessage(err), status: errorStatus(err) }
      }
    })
  }

  req.agents.forEach((agent, index) => {
    jobs.push(async () => {
      try {
        agents[index] = await senseAgent(agent, judgeFn)
      } catch (err) {
        agents[index] = {
          agentId: agent.agent.id,
          error: errorMessage(err),
          status: errorStatus(err),
        }
      }
    })
  })

  await runPool(jobs, req.concurrency)
  return {
    schemaVersion: 1,
    scene: { place: req.scene.place },
    world,
    agents,
    latencyMs: Date.now() - started,
  }
}

function composeAgent(args: {
  req: NormalizedSense
  compact: ReturnType<typeof compactAgent>
  result: JudgeResult
  started: number
  askAlly: boolean
}): Impulse {
  const answers = requireAnswers(args.result)
  const tactic = requireChoice(answers, 'tactic')
  if (!(tactic.choice in args.req.tactics)) {
    throw new IntuitionError(502, `Jev 返回了未声明的战术 ${tactic.choice}`)
  }
  const threat = requireScore(answers, 'threat')
  const interrupt = classifyInterrupt(
    readProbability(answers, 'interrupt'),
    args.req.policy.interruptAt,
  )
  const confidence = resolveConfidence(
    tactic.confidence,
    args.result.providerMetadata,
    'tactic',
    tactic.probabilities,
  )
  const holdKey = HOLD_TACTIC in args.req.tactics ? HOLD_TACTIC : null
  const decision = decideDisposition({
    current: args.req.agent.currentTactic,
    suggested: tactic.choice,
    confidence: confidence.confidence,
    interrupt,
    holdKey,
    switchConfidence: args.req.policy.switchConfidence,
  })

  const modelTargetId = modelTarget(answers, args.compact.roster)
  const candidates = args.compact.roster.map((entry) => ({
    id: entry.id,
    relation: entry.relation,
    distance: entry.distance,
    health: entry.health,
    kind: entry.kind,
  }))
  const kept = keepCurrentTarget(decision.disposition, decision.executing, args.req.agent.currentTargetId, args.compact.roster)
  const bound = kept ?? bindTarget(decision.executing, modelTargetId, candidates)
  const suggestedTactic =
    holdKey != null && tactic.choice === holdKey && args.req.agent.currentTactic
      ? args.req.agent.currentTactic
      : tactic.choice
  const suggested = bindTarget(suggestedTactic, modelTargetId, candidates)

  return {
    schemaVersion: 1,
    agentId: args.req.agent.id,
    role: args.req.agent.role,
    disposition: decision.disposition,
    tactic: decision.executing,
    suggestedTactic: tactic.choice,
    targetId: bound.targetId,
    suggestedTargetId: suggested.targetId,
    targetSource: bound.targetSource,
    because: decision.because,
    interrupt,
    opening: classifyBoolean(readProbability(answers, 'opening'), args.req.policy.uncertainMargin),
    openingKind: openingKindFor(args.req.agent.role),
    playerHostile: args.compact.hasPlayer
      ? classifyBoolean(readProbability(answers, 'playerHostile'), args.req.policy.uncertainMargin)
      : false,
    allyNeedsHelp: args.askAlly
      ? classifyBoolean(readProbability(answers, 'allyNeedsHelp'), args.req.policy.uncertainMargin)
      : false,
    threat: scoreBand(threat.score, THREAT_LEVELS),
    confidence: confidence.confidence,
    confidenceSource: confidence.source,
    probabilities: tactic.probabilities,
    modelId: readModelId(args.result, modelId()),
    usage: readUsage(args.result.usage),
    latencyMs: Date.now() - args.started,
  }
}

function composeWorld(args: {
  req: NormalizedWorld
  result: JudgeResult
  started: number
}): WorldImpulse {
  const answers = requireAnswers(args.result)
  const directive = requireChoice(answers, 'directive')
  if (!(directive.choice in args.req.directives)) {
    throw new IntuitionError(502, `Jev 返回了未声明的拍子 ${directive.choice}`)
  }
  const tension = requireScore(answers, 'tension')
  const confidence = resolveConfidence(
    directive.confidence,
    args.result.providerMetadata,
    'directive',
    directive.probabilities,
  )
  const holdKey = HOLD_DIRECTIVE in args.req.directives ? HOLD_DIRECTIVE : null
  const decision = decideDisposition({
    current: args.req.scene.currentDirective,
    suggested: directive.choice,
    confidence: confidence.confidence,
    interrupt: false,
    holdKey,
    switchConfidence: args.req.policy.switchConfidence,
  })

  return {
    schemaVersion: 1,
    disposition: decision.disposition,
    directive: decision.executing,
    suggestedDirective: directive.choice,
    because: decision.because,
    tension: scoreBand(tension.score, TENSION_LEVELS),
    playerOverextended: args.req.player
      ? classifyBoolean(readProbability(answers, 'overextended'), args.req.policy.uncertainMargin)
      : false,
    confidence: confidence.confidence,
    confidenceSource: confidence.source,
    probabilities: directive.probabilities,
    modelId: readModelId(args.result, modelId()),
    usage: readUsage(args.result.usage),
    latencyMs: Date.now() - args.started,
  }
}

function incapacitated(req: NormalizedSense, started: number): Impulse {
  return {
    schemaVersion: 1,
    agentId: req.agent.id,
    role: req.agent.role,
    disposition: 'incapacitated',
    tactic: 'none',
    suggestedTactic: 'none',
    targetId: null,
    suggestedTargetId: null,
    targetSource: 'none',
    because: 'incapacitated',
    interrupt: false,
    opening: false,
    openingKind: openingKindFor(req.agent.role),
    playerHostile: false,
    allyNeedsHelp: false,
    threat: scoreBand(THREAT_LEVELS.length - 1, THREAT_LEVELS),
    confidence: null,
    confidenceSource: 'none',
    probabilities: null,
    modelId: 'skipped',
    latencyMs: Date.now() - started,
  }
}

function keepCurrentTarget(
  disposition: 'continue' | 'switch' | 'hold',
  tactic: string,
  currentTargetId: string | null | undefined,
  roster: RosterEntry[],
): { targetId: string; targetSource: TargetSource } | null {
  if (disposition !== 'continue' && disposition !== 'hold') return null
  if (!tacticNeedsTarget(tactic) || !currentTargetId) return null
  if (!roster.some((entry) => entry.id === currentTargetId)) return null
  return { targetId: currentTargetId, targetSource: 'kept' }
}

function modelTarget(answers: Record<string, unknown>, roster: RosterEntry[]): string {
  const target = readChoice(answers, 'target')
  if (!target || target.choice === 'none') return 'none'
  return roster.find((entry) => entry.choiceKey === target.choice)?.id ?? 'none'
}

function requireAnswers(result: JudgeResult): Record<string, unknown> {
  const answers = asRecord(result.answers)
  if (!answers) throw new IntuitionError(502, 'Jev 返回里没有 answers')
  return answers
}

function requireChoice(answers: Record<string, unknown>, id: string) {
  const choice = readChoice(answers, id)
  if (!choice) {
    throw new IntuitionError(502, `Jev 没有返回 ${id}。答案键：${Object.keys(answers).join(', ') || '无'}`)
  }
  return choice
}

function requireScore(answers: Record<string, unknown>, id: string) {
  const score = readScore(answers, id)
  if (!score) throw new IntuitionError(502, `Jev 没有返回 ${id}`)
  return score
}

async function runPool(jobs: Array<() => Promise<void>>, limit: number): Promise<void> {
  const queue = [...jobs]
  const workers = Math.min(limit, queue.length)
  await Promise.all(
    Array.from({ length: workers }, async () => {
      while (queue.length > 0) {
        const job = queue.shift()
        if (job) await job()
      }
    }),
  )
}
