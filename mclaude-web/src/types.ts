// ─── Transport types ────────────────────────────────────────────────────────

export interface NATSConnectionOptions {
  url: string
  jwt: string
  nkeySeed: string
}

export interface NATSMessage {
  subject: string
  data: Uint8Array
  headers?: Record<string, string>
  reply?: string
  seq?: number // JetStream sequence number
}

export interface KVEntry {
  key: string
  value: Uint8Array
  revision: number
  operation?: 'PUT' | 'DEL' | 'PURGE'
}

export interface INATSClient {
  connect(opts: NATSConnectionOptions): Promise<void>
  reconnect(newJwt: string): Promise<void>
  subscribe(subject: string, callback: (msg: NATSMessage) => void): () => void
  /** Subscribe via JetStream ordered consumer with replay from startSeq. */
  jsSubscribe(stream: string, subject: string, startSeq: number, callback: (msg: NATSMessage) => void): Promise<() => void>
  publish(subject: string, data: Uint8Array, headers?: Record<string, string>): void
  request(subject: string, data: Uint8Array, timeoutMs?: number): Promise<NATSMessage>
  kvWatch(bucket: string, key: string, callback: (entry: KVEntry) => void): () => void
  kvGet(bucket: string, key: string): Promise<KVEntry | null>
  onDisconnect(callback: () => void): () => void
  onReconnect(callback: () => void): () => void
  isConnected(): boolean
  close(): Promise<void>
}

export interface AuthTokens {
  jwt: string
  nkeySeed: string
  userId: string
  natsUrl?: string
}

export interface IAuthClient {
  login(email: string, password: string): Promise<AuthTokens>
  loginSSO(provider: string): Promise<string> // redirect URL
  refresh(): Promise<{ jwt: string }>
  logout(): Promise<void>
  getStoredTokens(): AuthTokens | null
  storeTokens(tokens: AuthTokens): void
  clearTokens(): void
  getMe(): Promise<MeResponse>
  getAdminProviders(): Promise<AdminProvider[]>
  startOAuthConnect(providerId: string, returnUrl: string): Promise<string>
  getConnectionRepos(connectionId: string, query: string, page?: number): Promise<RepoListResponse>
  disconnectConnection(connectionId: string): Promise<void>
  addPAT(baseUrl: string, displayName: string, token: string): Promise<{ connectionId: string; providerType: string; displayName: string; username: string }>
  updateProjectIdentity(projectId: string, connectionId: string | null): Promise<void>
}

// ─── Session / Project state ─────────────────────────────────────────────────

export interface ControlRequest {
  requestId: string
  toolName: string
  input: unknown
}

export interface UsageStats {
  inputTokens: number
  outputTokens: number
  cacheReadTokens: number
  cacheWriteTokens: number
  costUsd: number
}

export interface Capabilities {
  skills: string[]
  tools: string[]
  agents: string[]
}

export type SessionState =
  | 'idle'
  | 'running'
  | 'requires_action'
  | 'plan_mode'
  | 'restarting'
  | 'failed'
  | 'updating'
  | 'unknown'
  | 'waiting_for_input'

export interface SessionKVState {
  id: string
  projectId: string
  branch: string
  worktree: string
  cwd: string
  name: string
  state: SessionState
  stateSince: string
  model: string
  capabilities: Capabilities
  pendingControls: Record<string, ControlRequest>
  usage: UsageStats
  replayFromSeq: number | null
}

export interface ProjectKVState {
  id: string
  name: string
  gitUrl: string
  gitIdentityId?: string | null
  status: string
  createdAt: string
}

// ─── Git provider / connection types ─────────────────────────────────────────

export interface ConnectedProvider {
  connectionId: string
  providerId: string
  providerType: 'github' | 'gitlab'
  authType: 'oauth' | 'pat'
  displayName: string
  baseUrl: string
  username: string
  connectedAt: string
}

export interface AdminProvider {
  id: string
  type: 'github' | 'gitlab'
  displayName: string
  baseUrl: string
  source: 'admin'
}

export interface Repo {
  name: string
  fullName: string
  private: boolean
  description: string
  cloneUrl: string
  updatedAt: string
}

export interface RepoListResponse {
  repos: Repo[]
  nextPage: number | null
  hasMore: boolean
}

export interface MeResponse {
  userId: string
  email: string
  name: string
  connectedProviders: ConnectedProvider[]
}

// ─── Stream-JSON event types ──────────────────────────────────────────────────

export interface BaseEvent {
  type: string
  parent_tool_use_id?: string | null
}

export interface SystemInitEvent extends BaseEvent {
  type: 'system'
  subtype: 'init'
  session_id: string
  model: string
  tools: string[]
  capabilities: Capabilities
}

export interface SystemStateChangedEvent extends BaseEvent {
  type: 'system'
  subtype: 'session_state_changed'
  state: SessionState
}

export interface SystemGenericEvent extends BaseEvent {
  type: 'system'
  subtype: string
  [key: string]: unknown
}

export interface StreamEvent extends BaseEvent {
  type: 'stream_event'
  stream_event: {
    type: string
    delta?: {
      type: string
      text?: string
    }
    index?: number
  }
}

export interface ContentBlock {
  type: 'text' | 'thinking' | 'tool_use' | 'tool_result'
  id?: string
  name?: string
  input?: unknown
  text?: string
  tool_use_id?: string
  content?: string | ContentBlock[]
  is_error?: boolean
}

export interface AssistantEvent extends BaseEvent {
  type: 'assistant'
  message: {
    id: string
    role: 'assistant'
    content: ContentBlock[]
    model: string
    usage?: {
      input_tokens: number
      output_tokens: number
      cache_read_input_tokens?: number
      cache_creation_input_tokens?: number
    }
  }
}

export interface UserEvent extends BaseEvent {
  type: 'user'
  message: {
    role: 'user'
    content: string | ContentBlock[]
  }
  uuid?: string         // round-tripped from stdin; present on --replay-user-messages echoes
  isReplay?: boolean    // true for all echoed messages
  isSynthetic?: boolean // true for task-notifications and coordinator messages
}

export interface ControlRequestEvent extends BaseEvent {
  type: 'control_request'
  subtype: 'can_use_tool'
  request_id: string
  tool_name: string
  input: unknown
}

export interface ToolProgressEvent extends BaseEvent {
  type: 'tool_progress'
  tool_use_id: string
  elapsed_ms: number
}

export interface ResultEvent extends BaseEvent {
  type: 'result'
  subtype: 'success' | 'error'
  usage?: UsageStats
}

export interface ClearEvent extends BaseEvent {
  type: 'clear'
}

export interface CompactBoundaryEvent extends BaseEvent {
  type: 'compact_boundary'
  summary: string
}

export interface KeepAliveEvent extends BaseEvent {
  type: 'keep_alive'
}

export type StreamJsonEvent =
  | SystemInitEvent
  | SystemStateChangedEvent
  | SystemGenericEvent
  | StreamEvent
  | AssistantEvent
  | UserEvent
  | ControlRequestEvent
  | ToolProgressEvent
  | ResultEvent
  | ClearEvent
  | CompactBoundaryEvent
  | KeepAliveEvent

// ─── Conversation model (accumulated from events) ────────────────────────────

export interface TextBlock {
  type: 'text'
  text: string
}

export interface StreamingTextBlock {
  type: 'streaming_text'
  chunks: string[]
  complete: boolean
}

export interface ToolUseBlock {
  type: 'tool_use'
  id: string
  name: string
  inputSummary: string
  fullInput?: unknown
  elapsed?: number
  result?: ToolResultBlock
}

export interface ToolResultBlock {
  type: 'tool_result'
  toolUseId: string
  content: string
  isError: boolean
}

export interface ThinkingBlock {
  type: 'thinking'
  text: string
}

export interface ControlRequestBlock {
  type: 'control_request'
  requestId: string
  toolName: string
  input: unknown
  status: 'pending' | 'approved' | 'denied'
}

export interface SystemMessageBlock {
  type: 'system_message'
  text: string
}

export interface CompactionBlock {
  type: 'compaction'
  summary: string
}

export interface SkillInvocationBlock {
  type: 'skill_invocation'
  skillName: string
  args: string
  rawContent: string
}

export type Block =
  | TextBlock
  | StreamingTextBlock
  | ToolUseBlock
  | ToolResultBlock
  | ThinkingBlock
  | ControlRequestBlock
  | CompactionBlock
  | SystemMessageBlock
  | SkillInvocationBlock

export interface PendingMessage {
  uuid: string
  content: string | Array<{ type: string; text?: string }>
  sentAt: number  // Date.now() for ordering
}

export interface Turn {
  id: string
  type: 'user' | 'assistant' | 'system'
  blocks: Block[]
  model?: string
  usage?: UsageStats
  parentToolUseId?: string
  /** Set on optimistic user turns added by addPendingMessage; cleared when server echo arrives. */
  pendingUuid?: string
}

export interface ConversationModel {
  turns: Turn[]
}

// ─── Lifecycle events ─────────────────────────────────────────────────────────

export interface LifecycleEvent {
  type: 'session_created' | 'session_stopped' | 'session_restarting' | 'session_failed' | 'session_upgrading'
  sessionId: string
  projectId: string
  timestamp: string
}
