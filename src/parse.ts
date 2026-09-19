import type { ConfidenceSource, TokenUsage } from './types'

export function asRecord(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  return value as Record<string, unknown>
}

export interface ChoiceAnswer {
  choice: string
  probabilities: Record<string, number> | null
  confidence: number | null
}

export function readChoice(answers: Record<string, unknown>, id: string): ChoiceAnswer | null {
  const rec = asRecord(answers[id])
  if (!rec || typeof rec.choice !== 'string' || !rec.choice) return null
  return {
    choice: rec.choice,
    probabilities: readDistribution(rec.probabilities),
    confidence: readUnit(rec.confidence),
  }
}

export function readScore(
  answers: Record<string, unknown>,
  id: string,
): { score: number; probabilities: Record<string, number> | null } | null {
  const rec = asRecord(answers[id])
  if (!rec || typeof rec.score !== 'number' || Number.isNaN(rec.score)) return null
  return { score: rec.score, probabilities: readDistribution(rec.probabilities) }
}

export function readProbability(answers: Record<string, unknown>, id: string): number | null {
  const rec = asRecord(answers[id])
  if (!rec) return null
  return readUnit(rec.probability) ?? readUnit(rec.noul)
}

export function resolveConfidence(
  answerConfidence: number | null,
  metadata: unknown,
  id: string,
  probabilities: Record<string, number> | null,
): { confidence: number | null; source: ConfidenceSource } {
  if (answerConfidence != null) return { confidence: answerConfidence, source: 'answer' }
  const meta = readMetaConfidence(metadata, id)
  if (meta != null) return { confidence: meta, source: 'typesafe' }
  const margin = confidenceFromDistribution(probabilities)
  if (margin != null) return { confidence: margin, source: 'margin' }
  return { confidence: null, source: 'none' }
}

export function readUsage(usage: unknown): TokenUsage | undefined {
  const rec = asRecord(usage)
  if (!rec) return undefined
  const inputTokens = readCount(rec.inputTokens) ?? readCount(rec.promptTokens)
  const outputTokens = readCount(rec.outputTokens) ?? readCount(rec.completionTokens)
  const totalTokens =
    readCount(rec.totalTokens) ??
    (inputTokens != null && outputTokens != null ? inputTokens + outputTokens : undefined)
  if (inputTokens == null && outputTokens == null && totalTokens == null) return undefined
  return { inputTokens, outputTokens, totalTokens }
}

export function readModelId(result: { response?: unknown; modelId?: unknown }, fallback: string): string {
  if (typeof result.modelId === 'string' && result.modelId) return result.modelId
  const response = asRecord(result.response)
  if (response && typeof response.modelId === 'string' && response.modelId) return response.modelId
  if (response && typeof response.id === 'string' && response.id.includes('jev')) return response.id
  return fallback
}

function readMetaConfidence(metadata: unknown, id: string): number | null {
  const root = asRecord(metadata)
  if (!root) return null
  const gateway = asRecord(root.gateway)
  const buckets = [root.typesafe, root['typesafe-ai'], gateway?.typesafe]
  for (const bucket of buckets) {
    const rec = asRecord(bucket)
    const confidence = asRecord(rec?.confidence)
    const value = confidence ? readUnit(confidence[id]) : null
    if (value != null) return value
  }
  return null
}

function confidenceFromDistribution(probabilities: Record<string, number> | null): number | null {
  if (!probabilities) return null
  const values = Object.values(probabilities).filter((n) => typeof n === 'number')
  if (values.length === 0) return null
  values.sort((a, b) => b - a)
  return Math.round((values[0] - (values[1] ?? 0)) * 100) / 100
}

function readDistribution(value: unknown): Record<string, number> | null {
  const rec = asRecord(value)
  if (!rec) return null
  const out: Record<string, number> = {}
  for (const [key, item] of Object.entries(rec)) {
    if (typeof item === 'number' && !Number.isNaN(item)) out[key] = item
  }
  return Object.keys(out).length > 0 ? out : null
}

function readUnit(value: unknown): number | null {
  if (typeof value !== 'number' || Number.isNaN(value) || value < 0 || value > 1) return null
  return value
}

function readCount(value: unknown): number | undefined {
  if (typeof value !== 'number' || !Number.isFinite(value)) return undefined
  return value
}
