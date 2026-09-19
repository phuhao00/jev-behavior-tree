import { senseTick } from './intuition'
import type { Entity, Impulse, TickResult, WorldImpulse } from './types'

interface Mem {
  tactic: string
  targetId: string | null
  seconds: number
}

interface Shock {
  title: string
  place: string
  weather: string
  events: string[]
  player: Entity
  dominance: string
  rookHealth: number
  miraHealth: number
  wolf?: { health: number; distance: number; visible: boolean }
}

const shocks: Shock[] = [
  {
    title: '旅人还在路上',
    place: 'ruined chapel door at dusk',
    weather: 'clear, wind in the trees',
    events: ['a single traveler appeared on the south road'],
    dominance: 'passing the chapel, not committed to entering',
    player: {
      id: 'player',
      kind: 'player',
      distance: 18,
      visible: true,
      health: 1,
      relation: 'neutral',
      activity: 'walking the south road, weapon sheathed',
    },
    rookHealth: 1,
    miraHealth: 1,
  },
  {
    title: '拔刀冲门',
    place: 'ruined chapel door at dusk',
    weather: 'clear, wind in the trees',
    events: ['the traveler drew a blade', 'the traveler sprinted at the chapel door'],
    dominance: 'committing to the door with a weapon out',
    player: {
      id: 'player',
      kind: 'player',
      distance: 5,
      visible: true,
      health: 1,
      relation: 'neutral',
      activity: 'sprinting at the door with a blade out',
    },
    rookHealth: 1,
    miraHealth: 1,
  },
  {
    title: '门口交手，林线里有狼',
    place: 'ruined chapel door at dusk',
    weather: 'clear, the trees have gone quiet',
    events: ['blades met at the chapel door', 'something heavy moved in the tree line'],
    dominance: 'deep in a fight at the threshold',
    player: {
      id: 'player',
      kind: 'player',
      distance: 2.5,
      visible: true,
      health: 0.85,
      relation: 'enemy',
      activity: 'in melee at the door, pressing the guard',
    },
    rookHealth: 0.42,
    miraHealth: 1,
    wolf: { health: 1, distance: 11, visible: false },
  },
  {
    title: '人跑了',
    place: 'ruined chapel door at dusk',
    weather: 'clear, wind in the trees',
    events: ['the traveler broke off and ran back down the road', 'the chapel door is still open'],
    dominance: 'leaving, badly hurt, the road behind them is open',
    player: {
      id: 'player',
      kind: 'player',
      distance: 22,
      visible: true,
      health: 0.35,
      relation: 'enemy',
      activity: 'running away down the road, no longer fighting',
    },
    rookHealth: 0.4,
    miraHealth: 1,
    wolf: { health: 1, distance: 16, visible: true },
  },
]

const memory: Record<string, Mem> = {
  rook: { tactic: 'patrol', targetId: null, seconds: 12 },
  mira: { tactic: 'interact', targetId: null, seconds: 20 },
  wolf: { tactic: 'hide', targetId: null, seconds: 5 },
}
let directive = 'hold_atmosphere'
let directiveSeconds = 40

const tacticZh: Record<string, string> = {
  hold: '保持',
  patrol: '巡逻',
  investigate: '查看',
  challenge: '警告',
  engage: '交战',
  flee: '撤离',
  hide: '隐蔽',
  assist: '援助',
  interact: '互动',
  none: '无',
  hold_atmosphere: '维持气氛',
  tighten_patrol: '收紧戒备',
  fog_stalk: '起雾潜行',
  ambush_now: '此刻伏击',
  release: '松开压力',
  seal_escape: '封死退路',
}

const becauseZh: Record<string, string> = {
  'first-decision': '第一次做决定',
  'still-fitting': '当前仍然合适',
  interrupt: '局面突变，打断',
  confident: '判断够清楚',
  hysteresis: '不够有把握，先别抖',
  incapacitated: '已经丧失行动',
}

function zh(key: string): string {
  return tacticZh[key] ?? key
}

function doing(tactic: string): string {
  const table: Record<string, string> = {
    hold: 'holding still',
    patrol: 'patrolling',
    investigate: 'moving in to look',
    challenge: 'shouting a warning',
    engage: 'fighting',
    flee: 'running away',
    hide: 'hiding',
    assist: 'moving to help an ally',
    interact: 'using something nearby',
  }
  return table[tactic] ?? 'hesitating'
}

function person(id: string, name: string, role: string, faction: string, health: number, extra: Entity[] = []) {
  const mem = memory[id]
  return {
    agent: {
      id,
      name,
      role,
      faction,
      health,
      activity: doing(mem.tactic),
      currentTactic: mem.tactic,
      currentTargetId: mem.targetId,
      secondsOnTactic: mem.seconds,
    },
    nearby: extra,
  }
}

function frame(shock: Shock) {
  const playerDistance = shock.player.distance ?? 0
  const miraDistance = Math.min(24, playerDistance + 3)
  const rook = person('rook', 'Rook', 'guard', 'chapel', shock.rookHealth, [
    {
      id: 'mira',
      name: 'Mira',
      kind: 'npc',
      faction: 'chapel',
      relation: 'ally',
      distance: 6,
      visible: true,
      health: shock.miraHealth,
      activity: doing(memory.mira.tactic),
    },
    ...(shock.wolf
      ? [
          {
            id: 'wolf',
            name: 'wolf',
            kind: 'creature' as const,
            faction: 'wild',
            relation: 'enemy' as const,
            distance: shock.wolf.distance,
            visible: shock.wolf.visible,
            health: shock.wolf.health,
            activity: doing(memory.wolf.tactic),
          },
        ]
      : []),
  ])
  const mira = person('mira', 'Mira', 'civilian', 'chapel', shock.miraHealth, [
    {
      id: 'rook',
      name: 'Rook',
      kind: 'npc' as const,
      faction: 'chapel',
      relation: 'ally' as const,
      distance: 6,
      visible: true,
      health: shock.rookHealth,
      activity: doing(memory.rook.tactic),
    },
    {
      id: 'candles',
      kind: 'prop' as const,
      distance: 1,
      activity: 'half-sorted candles on the floor',
    },
    ...(shock.wolf
      ? [
          {
            id: 'wolf',
            name: 'wolf',
            kind: 'creature' as const,
            faction: 'wild',
            relation: 'enemy' as const,
            distance: shock.wolf.distance + 3,
            visible: false,
            health: shock.wolf.health,
            activity: 'a shape in the trees',
          },
        ]
      : []),
  ])
  const agents = [
    { ...rook, player: { ...shock.player } },
    { ...mira, player: { ...shock.player, distance: miraDistance } },
  ]
  if (shock.wolf) {
    const wolf = person('wolf', 'wolf', 'predator', 'wild', shock.wolf.health, [
      {
        id: 'rook',
        name: 'Rook',
        kind: 'npc' as const,
        faction: 'chapel',
        relation: 'enemy' as const,
        distance: shock.wolf.distance,
        visible: true,
        health: shock.rookHealth,
        activity: doing(memory.rook.tactic),
      },
    ])
    agents.push({
      ...wolf,
      player: {
        ...shock.player,
        relation: 'enemy',
        distance: Math.max(4, playerDistance - 2),
      },
    })
  }

  return {
    scene: {
      place: shock.place,
      timeOfDay: 'dusk',
      weather: shock.weather,
      currentDirective: directive,
      secondsOnDirective: directiveSeconds,
      recentEvents: shock.events,
    },
    world: {
      player: {
        activity: shock.player.activity,
        health: shock.player.health,
        dominance: shock.dominance,
      },
      presences: [
        { id: 'rook', role: 'guard', health: shock.rookHealth, activity: doing(memory.rook.tactic), faction: 'chapel' },
        { id: 'mira', role: 'civilian', health: shock.miraHealth, activity: doing(memory.mira.tactic), faction: 'chapel' },
        ...(shock.wolf
          ? [{ id: 'wolf', role: 'predator', health: shock.wolf.health, activity: doing(memory.wolf.tactic), faction: 'wild' }]
          : []),
      ],
    },
    agents,
  }
}

function isImpulse(value: TickResult['agents'][number]): value is Impulse {
  return !('error' in value)
}

function isWorld(value: TickResult['world']): value is WorldImpulse {
  return value != null && !('error' in value)
}

function printImpulse(impulse: Impulse) {
  const target = impulse.targetId ? `，目标 ${impulse.targetId}` : ''
  console.log(
    `  ${impulse.agentId.padEnd(6)} ${zh(impulse.tactic).padEnd(4)} ${target}  [${becauseZh[impulse.because] ?? impulse.because}]`,
  )
  console.log(
    `         模型倾向 ${zh(impulse.suggestedTactic)}  置信 ${fmt(impulse.confidence)}（${impulse.confidenceSource}）  威胁 ${impulse.threat.score.toFixed(2)}`,
  )
  console.log(
    `         敌意 ${tri(impulse.playerHostile)}  求援 ${tri(impulse.allyNeedsHelp)}  时机/${impulse.openingKind} ${tri(impulse.opening)}  打断 ${tri(impulse.interrupt)}  ${impulse.latencyMs}ms`,
  )
}

function printWorld(world: WorldImpulse) {
  console.log(
    `  环境   ${zh(world.directive)}  [${becauseZh[world.because] ?? world.because}]  倾向 ${zh(world.suggestedDirective)}  张力 ${world.tension.score.toFixed(2)}  玩家冒进 ${tri(world.playerOverextended)}`,
  )
}

function tri(value: true | false | 'uncertain'): string {
  if (value === true) return '是'
  if (value === false) return '否'
  return '说不清'
}

function fmt(value: number | null): string {
  return value == null ? '无' : value.toFixed(2)
}

function remember(id: string, impulse: Impulse) {
  const prev = memory[id]
  memory[id] = {
    tactic: impulse.tactic,
    targetId: impulse.targetId,
    seconds: impulse.tactic === prev.tactic ? prev.seconds + 2 : 0,
  }
}

async function main() {
  if (!process.env.AI_GATEWAY_API_KEY) {
    console.error('缺少 AI_GATEWAY_API_KEY。')
    process.exit(1)
  }
  console.log('礼拜堂黄昏。玩家走位是脚本，战术由上一拍的结果喂回去。')
  let tokens = 0
  let failed = false

  for (const shock of shocks) {
    console.log(`\n—— ${shock.title} ——`)
    const result = await senseTick(frame(shock))
    if (isWorld(result.world)) {
      printWorld(result.world)
      tokens += result.world.usage?.inputTokens ?? 0
      if (result.world.directive !== directive) directiveSeconds = 0
      else directiveSeconds += 2
      directive = result.world.directive
    } else if (result.world && 'error' in result.world) {
      failed = true
      console.log(`  环境失败  ${result.world.error}`)
    }
    for (const agent of result.agents) {
      if (!isImpulse(agent)) {
        failed = true
        console.log(`  ${agent.agentId} 失败  ${agent.error}`)
        continue
      }
      printImpulse(agent)
      tokens += agent.usage?.inputTokens ?? 0
      remember(agent.agentId, agent)
    }
  }

  console.log(`\n闭环结束。输入约 ${tokens} tokens。执行层仍在游戏里：这里只换脑子，不播动画。`)
  if (failed) process.exit(1)
}

main().catch((err: unknown) => {
  console.error(err instanceof Error ? err.message : err)
  process.exit(1)
})
