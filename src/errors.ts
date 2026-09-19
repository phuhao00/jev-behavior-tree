export class IntuitionError extends Error {
  readonly status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'IntuitionError'
    this.status = status
  }
}

export function redact(message: string): string {
  return message
    .replace(/vck_[A-Za-z0-9_-]+/g, '[redacted]')
    .replace(/Bearer\s+\S+/gi, 'Bearer [redacted]')
}

export function errorStatus(err: unknown): number {
  return err instanceof IntuitionError ? err.status : 502
}

export function errorMessage(err: unknown): string {
  const message = err instanceof Error ? err.message : '未知错误'
  return redact(message).slice(0, 500)
}
