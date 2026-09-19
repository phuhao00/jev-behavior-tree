export type Vec3 = { x: number; y: number; z?: number }

export type Relation = 'ally' | 'enemy' | 'neutral' | 'unknown'

export type EntityKind = 'player' | 'npc' | 'creature' | 'prop' | 'hazard' | 'interest'

export interface Entity {
  id: string
  kind: EntityKind
  name?: string
  faction?: string
  relation?: Relation
  position?: Vec3
  /** 米。游戏算好再传，不要让模型做几何题。 */
  distance?: number
  visible?: boolean
  /** 0 到 1。 */
  health?: number
  activity?: string
  tags?: string[]
}

export interface AgentBlackboard {
  id: string
  /** 预置：guard、civilian、predator、companion、ambient。自定义角色必须自带 tactics。 */
  role: string
  name?: string
  personality?: string
  goal?: string
  faction?: string
  health?: number
  stamina?: number
  position?: Vec3
  activity?: string
  currentTactic?: string
  currentTargetId?: string | null
  secondsOnTactic?: number
  memory?: string[]
}

export interface SceneBlackboard {
  place: string
  timeOfDay?: string
  weather?: string
  currentDirective?: string
  secondsOnDirective?: number
  recentEvents?: string[]
}

export interface PolicyConfig {
  /** 新战术置信度达到此值就切换。默认 0.72。 */
  switchConfidence?: number
  /** interrupt 的 P(true) 达到此值，才允许在置信度不够时换战术。默认 0.75。 */
  interruptAt?: number
  /** 布尔概率落在 0.5±margin 内标成 uncertain。默认 0.12。 */
  uncertainMargin?: number
}

export interface SenseRequest {
  scene: SceneBlackboard
  agent: AgentBlackboard
  nearby?: Entity[]
  player?: Entity | null
  /**
   * 游戏这一拍真正能执行的战术。
   * 键是动画/行为 id，值是情境描述，不是键的同义词。
   * 不传则用角色预置包。自定义包会整个替换预置包。
   * 请保留 `hold`，表示「当前动作仍然合适」。
   */
  tactics?: Record<string, string>
  policy?: PolicyConfig
}

export interface Presence {
  id: string
  role: string
  health?: number
  activity?: string
  faction?: string
}

export interface WorldPlayer {
  activity?: string
  health?: number
  dominance?: string
}

export interface WorldRequest {
  scene: SceneBlackboard
  presences?: Presence[]
  player?: WorldPlayer | null
  /**
   * 环境这一拍能做的拍子。不传用预置。
   * 请保留 `hold_atmosphere`。不要把天气、刷怪、封门拆成多个 choice，
   * 那些问题彼此独立，会选出互相打架的答案。
   */
  directives?: Record<string, string>
  policy?: PolicyConfig
}

export interface TickRequest {
  scene: SceneBlackboard
  agents?: Array<Omit<SenseRequest, 'scene'> & { scene?: SceneBlackboard }>
  world?: boolean | Omit<WorldRequest, 'scene'>
  /** 1 到 8，默认 4。 */
  concurrency?: number
}

export type Tri = true | false | 'uncertain'

export type OpeningKind = 'ambush' | 'escape' | 'scout' | 'generic'

export type Because =
  | 'first-decision'
  | 'still-fitting'
  | 'interrupt'
  | 'confident'
  | 'hysteresis'
  | 'incapacitated'

export type TargetSource = 'model' | 'none' | 'geometric-fallback' | 'kept'

export type ConfidenceSource = 'answer' | 'typesafe' | 'margin' | 'none'

export interface ScoreRead {
  score: number
  level: number
  label: string
}

export interface TokenUsage {
  inputTokens?: number
  outputTokens?: number
  totalTokens?: number
}

export interface Impulse {
  schemaVersion: 1
  agentId: string
  role: string
  disposition: 'continue' | 'switch' | 'hold' | 'incapacitated'
  /** 游戏这一拍要执行的战术。hold 会被展开成上一拍的战术。 */
  tactic: string
  /** 模型原始选择，滞回时和 tactic 不同。 */
  suggestedTactic: string
  targetId: string | null
  suggestedTargetId: string | null
  targetSource: TargetSource
  because: Because
  interrupt: Tri
  opening: Tri
  openingKind: OpeningKind
  playerHostile: Tri
  allyNeedsHelp: Tri
  threat: ScoreRead
  confidence: number | null
  confidenceSource: ConfidenceSource
  probabilities: Record<string, number> | null
  modelId: string
  usage?: TokenUsage
  latencyMs: number
}

export interface WorldImpulse {
  schemaVersion: 1
  disposition: 'continue' | 'switch' | 'hold'
  directive: string
  suggestedDirective: string
  because: Because
  tension: ScoreRead
  playerOverextended: Tri
  confidence: number | null
  confidenceSource: ConfidenceSource
  probabilities: Record<string, number> | null
  modelId: string
  usage?: TokenUsage
  latencyMs: number
}

export interface AgentTickError {
  agentId: string
  error: string
  status: number
}

export interface TickResult {
  schemaVersion: 1
  scene: { place: string }
  world: WorldImpulse | { error: string; status: number } | null
  agents: Array<Impulse | AgentTickError>
  latencyMs: number
}

export interface RosterEntry {
  id: string
  choiceKey: string
  kind: EntityKind
  name: string
  faction?: string
  relation: Relation
  distance: number | null
  visible: boolean | null
  health: number | null
  activity: string
  tags: string[]
}
