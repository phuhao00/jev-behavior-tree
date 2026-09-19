import { experimental_evaluate as evaluate } from 'ai'
import { IntuitionError, redact } from './errors'

export interface JudgeResult {
  answers: Record<string, unknown>
  providerMetadata?: unknown
  response?: unknown
  modelId?: unknown
  usage?: unknown
}

export type Judge = (state: unknown, questions: Record<string, unknown>) => Promise<JudgeResult>

export function modelId(): string {
  return process.env.JEV_MODEL?.trim() || 'typesafe-ai/jev'
}

export async function judge(state: unknown, questions: Record<string, unknown>): Promise<JudgeResult> {
  if (!process.env.AI_GATEWAY_API_KEY) {
    throw new IntuitionError(500, '缺少环境变量 AI_GATEWAY_API_KEY')
  }

  const timeoutMs = positiveInt(process.env.JEV_TIMEOUT_MS, 20_000)
  const maxRetries = nonNegativeInt(process.env.JEV_MAX_RETRIES, 1)

  try {
    const result = await evaluate({
      model: modelId(),
      state: state as never,
      questions: questions as never,
      maxRetries,
      abortSignal: AbortSignal.timeout(timeoutMs),
      providerOptions: {
        gateway: { zeroDataRetention: true },
      },
    })
    const answers = result.answers
    if (!answers || typeof answers !== 'object') {
      throw new IntuitionError(502, 'Jev 返回里没有 answers')
    }
    return {
      answers: answers as Record<string, unknown>,
      providerMetadata: result.providerMetadata,
      response: result.response,
      usage: result.usage,
    }
  } catch (err) {
    if (err instanceof IntuitionError) throw err
    if (err instanceof Error && (err.name === 'TimeoutError' || err.name === 'AbortError')) {
      throw new IntuitionError(504, `Jev 调用超时（${timeoutMs}ms）`)
    }
    const statusCode = typeof (err as { statusCode?: unknown }).statusCode === 'number'
      ? (err as { statusCode: number }).statusCode
      : undefined
    const raw = err instanceof Error ? err.message : 'Jev 调用失败'
    if (statusCode === 401) {
      throw new IntuitionError(401, 'AI Gateway 拒绝了这个 key。请检查 AI_GATEWAY_API_KEY 是否仍有效。')
    }
    if (/credit card/i.test(raw)) {
      throw new IntuitionError(
        403,
        'Gateway key 是有效的，但这个 Vercel 账号还没有绑定信用卡，AI Gateway 拒绝了调用。到 Vercel 的 AI 页面加上卡并解锁免费额度后再试。',
      )
    }
    const status = statusCode && statusCode >= 400 && statusCode < 600 ? statusCode : 502
    throw new IntuitionError(status, redact(raw))
  }
}

function positiveInt(raw: string | undefined, fallback: number): number {
  const value = Number(raw ?? fallback)
  return Number.isFinite(value) && value > 0 ? value : fallback
}

function nonNegativeInt(raw: string | undefined, fallback: number): number {
  const value = Number(raw ?? fallback)
  return Number.isInteger(value) && value >= 0 ? value : fallback
}
