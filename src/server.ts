import { readFile } from 'node:fs/promises'
import { createServer, type IncomingMessage, type ServerResponse } from 'node:http'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { errorMessage, errorStatus, IntuitionError } from './errors'
import { modelId } from './jev'
import { senseAgent, senseTick, senseWorld } from './intuition'

const port = positivePort(process.env.PORT, 8787)
const host = process.env.HOST?.trim() || '127.0.0.1'
const pagePath = join(dirname(fileURLToPath(import.meta.url)), '..', 'public', 'index.html')

if (!process.env.AI_GATEWAY_API_KEY) {
  console.error('缺少 AI_GATEWAY_API_KEY。复制 .env.example 为 .env 后填入 Vercel AI Gateway 的 key。')
  process.exit(1)
}

const server = createServer((req, res) => {
  void handle(req, res)
})

server.listen(port, host, () => {
  console.log(`Jev 直觉服务  http://${host}:${port}`)
  console.log(`模型 ${modelId()}`)
  console.log('GET /              演示页')
  console.log('POST /v1/impulse   POST /v1/world   POST /v1/tick')
})

async function handle(req: IncomingMessage, res: ServerResponse) {
  try {
    if (req.method === 'OPTIONS') {
      writeJson(res, 204, null)
      return
    }
    const path = new URL(req.url ?? '/', 'http://127.0.0.1').pathname
    if (req.method === 'GET' && path === '/') {
      const html = await readFile(pagePath)
      res.writeHead(200, { 'content-type': 'text/html; charset=utf-8' })
      res.end(html)
      return
    }
    if (req.method === 'GET' && path === '/health') {
      writeJson(res, 200, {
        ok: true,
        service: 'jev-intuition',
        model: modelId(),
        endpoints: ['/health', '/v1/impulse', '/v1/world', '/v1/tick'],
      })
      return
    }
    if (req.method !== 'POST') {
      writeJson(res, 404, { error: '没有这个接口' })
      return
    }
    const body = await readJson(req)
    if (path === '/v1/impulse') {
      writeJson(res, 200, await senseAgent(body))
      return
    }
    if (path === '/v1/world') {
      writeJson(res, 200, await senseWorld(body))
      return
    }
    if (path === '/v1/tick') {
      writeJson(res, 200, await senseTick(body))
      return
    }
    writeJson(res, 404, { error: '没有这个接口' })
  } catch (err) {
    writeJson(res, errorStatus(err), { error: errorMessage(err) })
  }
}

function readJson(req: IncomingMessage): Promise<unknown> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = []
    let size = 0
    req.on('data', (chunk: Buffer) => {
      size += chunk.length
      if (size > 262_144) {
        reject(new IntuitionError(413, '请求体超过 256KB'))
        req.destroy()
        return
      }
      chunks.push(chunk)
    })
    req.on('end', () => {
      try {
        const text = Buffer.concat(chunks).toString('utf8')
        if (!text.trim()) {
          reject(new IntuitionError(400, '请求体为空'))
          return
        }
        resolve(JSON.parse(text) as unknown)
      } catch (err) {
        if (err instanceof IntuitionError) reject(err)
        else reject(new IntuitionError(400, '请求体不是合法 JSON'))
      }
    })
    req.on('error', () => reject(new IntuitionError(400, '读取请求失败')))
  })
}

function writeJson(res: ServerResponse, status: number, body: unknown) {
  const headers: Record<string, string> = {
    'access-control-allow-origin': '*',
    'access-control-allow-methods': 'GET, POST, OPTIONS',
    'access-control-allow-headers': 'content-type',
  }
  if (status === 204) {
    res.writeHead(status, headers)
    res.end()
    return
  }
  headers['content-type'] = 'application/json; charset=utf-8'
  res.writeHead(status, headers)
  res.end(JSON.stringify(body))
}

function positivePort(raw: string | undefined, fallback: number): number {
  const value = Number(raw ?? fallback)
  if (!Number.isInteger(value) || value <= 0 || value > 65535) return fallback
  return value
}
