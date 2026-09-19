import { IntuitionError } from './errors'
import type { Because, PolicyConfig, Relation, Tri } from './types.js'

export interface Policy {
  switchConfidence: number
  interruptAt: number
  uncertainMargin: number
}

export const DEFAULT_POLICY: Policy = {
  switchConfidence: 0.72,
  interruptAt: 0.75,
  uncertainMargin: 0.12,
}

export interface Decision {
  disposition: 'continue' | 'switch' | 'hold'
  executing: string
  because: Because
}

export interface TargetCandidate {
  id: string
  relation: Relation
  distance: number | null
  health: number | null
  kind: string
}

const NO_TARGET = new Set(['hold', 'patrol', 'hide', 'flee'])

export function resolvePolicy(input?: PolicyConfig): Policy {
  const policy: Policy = { ...DEFAULT_POLICY }
  if (!input) return policy
  for (const key of ['switchConfidence', 'interruptAt', 'uncertainMargin'] as const) {
    const value = input[key]
    if (value == null) continue
    if (typeof value !== 'number' || Number.isNaN(value) || value < 0 || value > 1) {
      throw new IntuitionError(400, `policy.${key} 必须是 0 到 1 之间的数字`)
    }
    policy[key] = value
  }
  return policy
}

export function classifyBoolean(probability: number | null, margin: number): Tri {
  if (probability == null || Number.isNaN(probability)) return 'uncertain'
  if (probability >= 0.5 + margin) return true
  if (probability <= 0.5 - margin) return false
  return 'uncertain'
}

export function classifyInterrupt(probability: number | null, interruptAt: number): Tri {
  if (probability == null || Number.isNaN(probability)) return 'uncertain'
  if (probability >= interruptAt) return true
  if (probability <= 1 - interruptAt) return false
  return 'uncertain'
}

/**
 * Jev 的每个问题互相独立，所以代码只做三件事：
 * 展开 hold、用 interrupt 放行一次切换、置信度不够就保持当前战术。
 * interrupt 不能推翻模型明确选中的 hold。
 */
export function decideDisposition(args: {
  current: string | null | undefined
  suggested: string
  confidence: number | null
  interrupt: Tri
  holdKey: string | null
  switchConfidence: number
}): Decision {
  const current = args.current?.trim() || null
  const expanded =
    args.holdKey != null && args.suggested === args.holdKey && current ? current : args.suggested

  if (!current) {
    return { disposition: 'switch', executing: expanded, because: 'first-decision' }
  }
  if (expanded === current) {
    return { disposition: 'continue', executing: current, because: 'still-fitting' }
  }
  if (args.interrupt === true) {
    return { disposition: 'switch', executing: expanded, because: 'interrupt' }
  }
  if (args.confidence == null || args.confidence >= args.switchConfidence) {
    return { disposition: 'switch', executing: expanded, because: 'confident' }
  }
  return { disposition: 'hold', executing: current, because: 'hysteresis' }
}

export function tacticNeedsTarget(tactic: string): boolean {
  return !NO_TARGET.has(tactic)
}

export function bindTarget(
  tactic: string,
  modelTargetId: string,
  roster: TargetCandidate[],
): { targetId: string | null; targetSource: 'model' | 'none' | 'geometric-fallback' } {
  if (!tacticNeedsTarget(tactic)) return { targetId: null, targetSource: 'none' }
  if (modelTargetId !== 'none' && roster.some((entry) => entry.id === modelTargetId)) {
    return { targetId: modelTargetId, targetSource: 'model' }
  }
  const fallback = fallbackTarget(tactic, roster)
  if (fallback) return { targetId: fallback, targetSource: 'geometric-fallback' }
  return { targetId: null, targetSource: 'none' }
}

export function scoreBand(
  score: number,
  labels: readonly string[],
): { score: number; level: number; label: string } {
  const max = Math.max(0, labels.length - 1)
  const clamped = Math.min(max, Math.max(0, score))
  const level = Math.min(max, Math.max(0, Math.round(clamped)))
  return { score: round2(clamped), level, label: labels[level] ?? labels[0] ?? 'unknown' }
}

function fallbackTarget(tactic: string, roster: TargetCandidate[]): string | null {
  const ranked = [...roster].sort((a, b) => (a.distance ?? 1e9) - (b.distance ?? 1e9))
  if (tactic === 'assist') {
    const allies = ranked
      .filter((entry) => entry.relation === 'ally')
      .sort(
        (a, b) =>
          (a.health ?? 1) - (b.health ?? 1) || (a.distance ?? 1e9) - (b.distance ?? 1e9),
      )
    return allies[0]?.id ?? null
  }
  if (tactic === 'investigate' || tactic === 'interact') {
    const prop = ranked.find(
      (entry) => entry.kind === 'interest' || entry.kind === 'hazard' || entry.kind === 'prop',
    )
    return prop?.id ?? ranked[0]?.id ?? null
  }
  return ranked.find((entry) => entry.relation === 'enemy')?.id ?? ranked[0]?.id ?? null
}

function round2(n: number): number {
  return Math.round(n * 100) / 100
}
