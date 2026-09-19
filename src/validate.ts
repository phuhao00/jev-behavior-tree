import { IntuitionError } from './errors'
import { resolvePolicy, type Policy } from './policy'
import { ROLES, WORLD_DIRECTIVES, tacticsForRole } from './roles'
import type {
  AgentBlackboard,
  Entity,
  EntityKind,
  PolicyConfig,
  Presence,
  Relation,
  SceneBlackboard,
  Vec3,
  WorldPlayer,
} from './types'

const ID = /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$/
const KEY = /^[A-Za-z][A-Za-z0-9_]{0,40}$/
const KINDS = new Set<EntityKind>(['player', 'npc', 'creature', 'prop', 'hazard', 'interest'])
const RELATIONS = new Set<Relation>(['ally', 'enemy', 'neutral', 'unknown'])

export interface NormalizedSense {
  scene: SceneBlackboard
  agent: AgentBlackboard
  nearby: Entity[]
  player: Entity | null
  tactics: Record<string, string>
  policy: Policy
}

export interface NormalizedWorld {
  scene: SceneBlackboard
  presences: Presence[]
  player: WorldPlayer | null
  directives: Record<string, string>
  policy: Policy
}

export interface NormalizedTick {
  scene: SceneBlackboard
  agents: NormalizedSense[]
  world: NormalizedWorld | null
  concurrency: number
}

export function validateSense(input: unknown): NormalizedSense {
  const raw = record(input, '请求体')
  const scene = validateScene(raw.scene)
  const agent = validateAgent(raw.agent)
  const nearby = validateNearby(raw.nearby)
  const player = raw.player == null ? null : validateEntity(raw.player, 'player', 'player')
  const tactics =
    raw.tactics == null ? tacticsForRole(agent.role) : validateOptionMap(raw.tactics, 'tactics', 1, 24)
  if (!tactics) {
    throw new IntuitionError(
      400,
      `角色 "${agent.role}" 没有预置战术。请传 tactics。预置角色：${ROLES.join('、')}`,
    )
  }
  return {
    scene,
    agent,
    nearby,
    player,
    tactics,
    policy: resolvePolicy(readPolicy(raw.policy)),
  }
}

export function validateWorld(input: unknown): NormalizedWorld {
  const raw = record(input, 'world')
  return {
    scene: validateScene(raw.scene),
    presences: validatePresences(raw.presences),
    player: raw.player == null ? null : validateWorldPlayer(raw.player),
    directives:
      raw.directives == null
        ? WORLD_DIRECTIVES
        : validateOptionMap(raw.directives, 'directives', 1, 24),
    policy: resolvePolicy(readPolicy(raw.policy)),
  }
}

export function validateTick(input: unknown): NormalizedTick {
  const raw = record(input, '请求体')
  const scene = validateScene(raw.scene)
  if (raw.agents != null && !Array.isArray(raw.agents)) {
    throw new IntuitionError(400, 'agents 必须是数组')
  }
  const agentsRaw = Array.isArray(raw.agents) ? raw.agents : []
  if (agentsRaw.length > 16) throw new IntuitionError(400, '单次 tick 最多 16 个 agent')
  const agents = agentsRaw.map((agent, index) => {
    const rec = record(agent, `agents[${index}]`)
    return validateSense({ ...rec, scene })
  })

  let world: NormalizedWorld | null = null
  if (raw.world === true) world = validateWorld({ scene })
  else if (raw.world != null && raw.world !== false) {
    const rec = record(raw.world, 'world')
    world = validateWorld({ ...rec, scene })
  }
  if (agents.length === 0 && !world) {
    throw new IntuitionError(400, 'tick 至少要有一个 agent，或把 world 设为 true')
  }
  return { scene, agents, world, concurrency: readConcurrency(raw.concurrency) }
}

function validateScene(input: unknown): SceneBlackboard {
  const raw = record(input, 'scene')
  const scene: SceneBlackboard = { place: requireText(raw.place, 'scene.place', 200) }
  const timeOfDay = optionalText(raw.timeOfDay, 'scene.timeOfDay', 40)
  const weather = optionalText(raw.weather, 'scene.weather', 80)
  const currentDirective = optionalText(raw.currentDirective, 'scene.currentDirective', 64)
  if (timeOfDay) scene.timeOfDay = timeOfDay
  if (weather) scene.weather = weather
  if (currentDirective) scene.currentDirective = currentDirective
  if (raw.secondsOnDirective != null) {
    scene.secondsOnDirective = requireNonNegative(raw.secondsOnDirective, 'scene.secondsOnDirective')
  }
  if (raw.recentEvents != null) scene.recentEvents = stringList(raw.recentEvents, 'scene.recentEvents', 6, 180)
  return scene
}

function validateAgent(input: unknown): AgentBlackboard {
  const raw = record(input, 'agent')
  const agent: AgentBlackboard = {
    id: requireId(raw.id, 'agent.id'),
    role: requireText(raw.role, 'agent.role', 40),
  }
  assignText(agent, 'name', raw.name, 40)
  assignText(agent, 'personality', raw.personality, 280)
  assignText(agent, 'goal', raw.goal, 200)
  assignText(agent, 'faction', raw.faction, 40)
  assignText(agent, 'activity', raw.activity, 160)
  assignText(agent, 'currentTactic', raw.currentTactic, 64)
  if (raw.health != null) agent.health = requireUnit(raw.health, 'agent.health')
  if (raw.stamina != null) agent.stamina = requireUnit(raw.stamina, 'agent.stamina')
  if (raw.position != null) agent.position = validateVec(raw.position, 'agent.position')
  if (raw.currentTargetId === null) agent.currentTargetId = null
  else if (raw.currentTargetId != null) agent.currentTargetId = requireId(raw.currentTargetId, 'agent.currentTargetId')
  if (raw.secondsOnTactic != null) {
    agent.secondsOnTactic = requireNonNegative(raw.secondsOnTactic, 'agent.secondsOnTactic')
  }
  if (raw.memory != null) agent.memory = stringList(raw.memory, 'agent.memory', 4, 160)
  return agent
}

function validateNearby(input: unknown): Entity[] {
  if (input == null) return []
  if (!Array.isArray(input)) throw new IntuitionError(400, 'nearby 必须是数组')
  if (input.length > 12) throw new IntuitionError(400, 'nearby 最多 12 个，请在游戏侧先筛掉远处的实体')
  return input.map((entity, index) => validateEntity(entity, `nearby[${index}]`, 'npc'))
}

function validateEntity(input: unknown, path: string, fallbackKind: EntityKind): Entity {
  const raw = record(input, path)
  const entity: Entity = {
    id: requireId(raw.id, `${path}.id`),
    kind: raw.kind == null ? fallbackKind : requireKind(raw.kind, `${path}.kind`),
  }
  assignEntityText(entity, 'name', raw.name, path, 40)
  assignEntityText(entity, 'faction', raw.faction, path, 40)
  assignEntityText(entity, 'activity', raw.activity, path, 160)
  if (raw.relation != null) entity.relation = requireRelation(raw.relation, `${path}.relation`)
  if (raw.distance != null) entity.distance = requireNonNegative(raw.distance, `${path}.distance`)
  if (raw.health != null) entity.health = requireUnit(raw.health, `${path}.health`)
  if (raw.visible != null) entity.visible = requireBoolean(raw.visible, `${path}.visible`)
  if (raw.position != null) entity.position = validateVec(raw.position, `${path}.position`)
  if (raw.tags != null) entity.tags = stringList(raw.tags, `${path}.tags`, 4, 32)
  return entity
}

function validatePresences(input: unknown): Presence[] {
  if (input == null) return []
  if (!Array.isArray(input)) throw new IntuitionError(400, 'presences 必须是数组')
  if (input.length > 16) throw new IntuitionError(400, 'presences 最多 16 个')
  return input.map((item, index) => {
    const raw = record(item, `presences[${index}]`)
    const presence: Presence = {
      id: requireId(raw.id, `presences[${index}].id`),
      role: requireText(raw.role, `presences[${index}].role`, 40),
    }
    const activity = optionalText(raw.activity, `presences[${index}].activity`, 160)
    const faction = optionalText(raw.faction, `presences[${index}].faction`, 40)
    if (activity) presence.activity = activity
    if (faction) presence.faction = faction
    if (raw.health != null) presence.health = requireUnit(raw.health, `presences[${index}].health`)
    return presence
  })
}

function validateWorldPlayer(input: unknown): WorldPlayer {
  const raw = record(input, 'player')
  const player: WorldPlayer = {}
  const activity = optionalText(raw.activity, 'player.activity', 160)
  const dominance = optionalText(raw.dominance, 'player.dominance', 160)
  if (activity) player.activity = activity
  if (dominance) player.dominance = dominance
  if (raw.health != null) player.health = requireUnit(raw.health, 'player.health')
  return player
}

function validateVec(input: unknown, path: string): Vec3 {
  const raw = record(input, path)
  const vec: Vec3 = {
    x: requireFinite(raw.x, `${path}.x`),
    y: requireFinite(raw.y, `${path}.y`),
  }
  if (raw.z != null) vec.z = requireFinite(raw.z, `${path}.z`)
  return vec
}

export function validateOptionMap(
  input: unknown,
  path: string,
  min: number,
  max: number,
): Record<string, string> {
  const raw = record(input, path)
  const entries = Object.entries(raw)
  if (entries.length < min || entries.length > max) {
    throw new IntuitionError(400, `${path} 需要 ${min} 到 ${max} 个选项`)
  }
  const out: Record<string, string> = {}
  for (const [key, value] of entries) {
    if (!KEY.test(key)) {
      throw new IntuitionError(400, `${path}.${key} 的 id 需要以字母开头，只能含英文、数字和下划线`)
    }
    if (typeof value !== 'string' || !value.trim()) {
      throw new IntuitionError(400, `${path}.${key} 需要一段情境描述，不能为空`)
    }
    if (value.length > 500) throw new IntuitionError(400, `${path}.${key} 的描述超过 500 字`)
    out[key] = value.trim()
  }
  return out
}

function readPolicy(input: unknown): PolicyConfig | undefined {
  if (input == null) return undefined
  return record(input, 'policy') as PolicyConfig
}

function readConcurrency(input: unknown): number {
  if (input == null) return 4
  if (typeof input !== 'number' || !Number.isInteger(input) || input < 1 || input > 8) {
    throw new IntuitionError(400, 'concurrency 必须是 1 到 8 的整数')
  }
  return input
}

function record(input: unknown, path: string): Record<string, unknown> {
  if (!input || typeof input !== 'object' || Array.isArray(input)) {
    throw new IntuitionError(400, `${path} 必须是对象`)
  }
  return input as Record<string, unknown>
}

function requireId(input: unknown, path: string): string {
  if (typeof input !== 'string' || !ID.test(input)) {
    throw new IntuitionError(400, `${path} 需要 1 到 64 位的英文、数字、_ . : -`)
  }
  return input
}

function requireText(input: unknown, path: string, max: number): string {
  if (typeof input !== 'string' || !input.trim()) throw new IntuitionError(400, `${path} 不能为空`)
  if (input.trim().length > max) throw new IntuitionError(400, `${path} 超过 ${max} 字`)
  return input.trim()
}

function optionalText(input: unknown, path: string, max: number): string | undefined {
  if (input == null) return undefined
  return requireText(input, path, max)
}

function requireUnit(input: unknown, path: string): number {
  const value = requireFinite(input, path)
  if (value < 0 || value > 1) throw new IntuitionError(400, `${path} 必须在 0 到 1 之间`)
  return value
}

function requireNonNegative(input: unknown, path: string): number {
  const value = requireFinite(input, path)
  if (value < 0) throw new IntuitionError(400, `${path} 不能是负数`)
  return value
}

function requireFinite(input: unknown, path: string): number {
  if (typeof input !== 'number' || !Number.isFinite(input)) {
    throw new IntuitionError(400, `${path} 必须是有限数字`)
  }
  return input
}

function requireBoolean(input: unknown, path: string): boolean {
  if (typeof input !== 'boolean') throw new IntuitionError(400, `${path} 必须是布尔值`)
  return input
}

function requireKind(input: unknown, path: string): EntityKind {
  if (typeof input !== 'string' || !KINDS.has(input as EntityKind)) {
    throw new IntuitionError(400, `${path} 必须是 ${[...KINDS].join('、')}`)
  }
  return input as EntityKind
}

function requireRelation(input: unknown, path: string): Relation {
  if (typeof input !== 'string' || !RELATIONS.has(input as Relation)) {
    throw new IntuitionError(400, `${path} 必须是 ally、enemy、neutral 或 unknown`)
  }
  return input as Relation
}

function stringList(input: unknown, path: string, max: number, chars: number): string[] {
  if (!Array.isArray(input)) throw new IntuitionError(400, `${path} 必须是字符串数组`)
  return input.slice(-max).map((item, index) => requireText(item, `${path}[${index}]`, chars))
}

function assignText(
  agent: AgentBlackboard,
  key: 'name' | 'personality' | 'goal' | 'faction' | 'activity' | 'currentTactic',
  value: unknown,
  max: number,
) {
  const text = optionalText(value, `agent.${key}`, max)
  if (text) agent[key] = text
}

function assignEntityText(
  entity: Entity,
  key: 'name' | 'faction' | 'activity',
  value: unknown,
  path: string,
  max: number,
) {
  const text = optionalText(value, `${path}.${key}`, max)
  if (text) entity[key] = text
}
