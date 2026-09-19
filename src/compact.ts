import { priorForRole } from './roles'
import type {
  AgentBlackboard,
  Entity,
  EntityKind,
  Relation,
  RosterEntry,
  SceneBlackboard,
  Vec3,
  WorldPlayer,
  Presence,
} from './types'

export interface CompactAgent {
  state: Record<string, unknown>
  roster: RosterEntry[]
  hasPlayer: boolean
}

export function compactAgent(
  args: {
    scene: SceneBlackboard
    agent: AgentBlackboard
    player: Entity | null
    nearby: Entity[]
  },
): CompactAgent {
  const prior = priorForRole(args.agent.role)
  const roster = buildRoster(args.agent, args.player, args.nearby)
  const playerEntry = args.player ? roster.find((entry) => entry.id === args.player?.id) : undefined
  const nearbyEntries = roster.filter((entry) => entry.id !== args.player?.id)

  const state = omitEmpty({
    scene: omitEmpty({
      place: args.scene.place,
      timeOfDay: args.scene.timeOfDay,
      weather: args.scene.weather,
      recentEvents: clipList(args.scene.recentEvents, 6, 180),
    }),
    agent: omitEmpty({
      id: args.agent.id,
      name: args.agent.name,
      role: args.agent.role,
      personality: args.agent.personality ?? prior?.personality,
      goal: args.agent.goal ?? prior?.goal,
      faction: args.agent.faction,
      health: args.agent.health,
      stamina: args.agent.stamina,
      activity: args.agent.activity,
      currentTactic: args.agent.currentTactic,
      currentTargetId: args.agent.currentTargetId ?? undefined,
      secondsOnTactic: args.agent.secondsOnTactic,
      memory: clipList(args.agent.memory, 4, 160),
    }),
    player: playerEntry ? publicEntry(playerEntry) : undefined,
    nearby: nearbyEntries.map(publicEntry),
  })

  return { state, roster, hasPlayer: playerEntry != null }
}

export function compactWorld(args: {
  scene: SceneBlackboard
  presences: Presence[]
  player: WorldPlayer | null
}): Record<string, unknown> {
  return omitEmpty({
    scene: omitEmpty({
      place: args.scene.place,
      timeOfDay: args.scene.timeOfDay,
      weather: args.scene.weather,
      currentDirective: args.scene.currentDirective,
      secondsOnDirective: args.scene.secondsOnDirective,
      recentEvents: clipList(args.scene.recentEvents, 6, 180),
    }),
    player: args.player
      ? omitEmpty({
          activity: args.player.activity,
          health: args.player.health,
          dominance: args.player.dominance,
        })
      : undefined,
    presences: args.presences.map((presence) =>
      omitEmpty({
        id: presence.id,
        role: presence.role,
        health: presence.health,
        activity: presence.activity,
        faction: presence.faction,
      }),
    ),
  })
}

function buildRoster(agent: AgentBlackboard, player: Entity | null, nearby: Entity[]): RosterEntry[] {
  const rows: Entity[] = []
  if (player && player.id !== agent.id) rows.push(player)
  const sorted = nearby
    .filter((entity) => entity.id !== agent.id && entity.id !== player?.id)
    .sort((a, b) => measuredDistance(agent, a) - measuredDistance(agent, b))
  rows.push(...sorted.slice(0, 12))

  const used = new Set(['none'])
  return rows.slice(0, 16).map((entity) => toEntry(agent, entity, used))
}

function toEntry(agent: AgentBlackboard, entity: Entity, used: Set<string>): RosterEntry {
  const distance = measuredDistance(agent, entity)
  return {
    id: entity.id,
    choiceKey: toChoiceKey(entity.id, used),
    kind: entity.kind,
    name: entity.name?.trim() || entity.id,
    faction: entity.faction,
    relation: relationOf(agent, entity),
    distance: Number.isFinite(distance) && distance < 1e8 ? round1(distance) : null,
    visible: typeof entity.visible === 'boolean' ? entity.visible : null,
    health: typeof entity.health === 'number' ? entity.health : null,
    activity: entity.activity?.trim().slice(0, 120) ?? '',
    tags: (entity.tags ?? []).filter((tag) => tag.trim()).slice(0, 4).map((tag) => tag.trim().slice(0, 32)),
  }
}

function relationOf(agent: AgentBlackboard, entity: Entity): Relation {
  if (entity.relation) return entity.relation
  if (agent.faction && entity.faction && agent.faction === entity.faction) return 'ally'
  return 'unknown'
}

function measuredDistance(agent: AgentBlackboard, entity: Entity): number {
  if (typeof entity.distance === 'number') return entity.distance
  const computed = vecDistance(agent.position, entity.position)
  return computed ?? 1e9
}

function vecDistance(a?: Vec3, b?: Vec3): number | null {
  if (!a || !b) return null
  return Math.hypot(a.x - b.x, a.y - b.y, (a.z ?? 0) - (b.z ?? 0))
}

function toChoiceKey(id: string, used: Set<string>): string {
  let base = id.replace(/[^A-Za-z0-9_]/g, '_').replace(/^[^A-Za-z]+/, '').slice(0, 40)
  if (!base || base === 'none') base = 'entity'
  let key = base
  let n = 2
  while (used.has(key)) key = `${base.slice(0, 36)}_${n++}`
  used.add(key)
  return key
}

function publicEntry(entry: RosterEntry): Record<string, unknown> {
  return omitEmpty({
    id: entry.choiceKey,
    name: entry.name !== entry.choiceKey ? entry.name : undefined,
    kind: entry.kind satisfies EntityKind,
    faction: entry.faction,
    relation: entry.relation,
    distance: entry.distance,
    visible: entry.visible,
    health: entry.health,
    activity: entry.activity,
    tags: entry.tags,
  })
}

function clipList(list: string[] | undefined, max: number, chars: number): string[] {
  if (!list) return []
  return list
    .filter((item) => item.trim())
    .slice(-max)
    .map((item) => item.trim().slice(0, chars))
}

function omitEmpty(input: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const [key, value] of Object.entries(input)) {
    if (value === undefined || value === null || value === '') continue
    if (Array.isArray(value) && value.length === 0) continue
    out[key] = value
  }
  return out
}

function round1(n: number): number {
  return Math.round(n * 10) / 10
}
