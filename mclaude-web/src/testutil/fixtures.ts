import type {
  SessionKVState,
  ProjectKVState,
  ConversationModel,
  StreamJsonEvent,
  Turn,
  TextBlock,
  ToolUseBlock,
  ControlRequestBlock,
} from '@/types'

// ─── Session / Project fixtures ──────────────────────────────────────────────

export function makeSessionKVState(overrides: Partial<SessionKVState> = {}): SessionKVState {
  return {
    id: 'session-1',
    projectId: 'project-1',
    branch: 'main',
    worktree: '/worktrees/session-1',
    cwd: '/repo',
    name: 'Test Session',
    state: 'idle',
    stateSince: new Date().toISOString(),
    model: 'claude-sonnet-4-6',
    capabilities: { skills: ['commit', 'review-pr'], tools: ['Bash', 'Read'], agents: [] },
    pendingControls: {},
    usage: { inputTokens: 100, outputTokens: 50, cacheReadTokens: 0, cacheWriteTokens: 0, costUsd: 0.001 },
    replayFromSeq: null,
    ...overrides,
  }
}

export function makeProjectKVState(overrides: Partial<ProjectKVState> = {}): ProjectKVState {
  return {
    id: 'project-1',
    name: 'Test Project',
    gitUrl: 'https://github.com/example/repo',
    status: 'active',
    createdAt: new Date().toISOString(),
    ...overrides,
  }
}

// ─── Stream-JSON event sequences ─────────────────────────────────────────────

export const transcripts = {
  // Simple: user message → assistant text response
  simpleMessage: [
    {
      type: 'system',
      subtype: 'init',
      session_id: 'session-1',
      model: 'claude-sonnet-4-6',
      tools: ['Bash', 'Read'],
      capabilities: { skills: ['commit'], tools: ['Bash', 'Read'], agents: [] },
    } satisfies StreamJsonEvent,
    {
      type: 'user',
      message: { role: 'user', content: 'Hello, Claude!' },
    } satisfies StreamJsonEvent,
    {
      type: 'stream_event',
      stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'Hello' }, index: 0 },
    } satisfies StreamJsonEvent,
    {
      type: 'stream_event',
      stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: ', world!' }, index: 0 },
    } satisfies StreamJsonEvent,
    {
      type: 'assistant',
      message: {
        id: 'msg-1',
        role: 'assistant',
        content: [{ type: 'text', text: 'Hello, world!' }],
        model: 'claude-sonnet-4-6',
        usage: { input_tokens: 10, output_tokens: 5 },
      },
    } satisfies StreamJsonEvent,
    {
      type: 'result',
      subtype: 'success',
      usage: { inputTokens: 10, outputTokens: 5, cacheReadTokens: 0, cacheWriteTokens: 0, costUsd: 0.0001 },
    } satisfies StreamJsonEvent,
  ] as StreamJsonEvent[],

  // Tool use: user message → tool_use → tool_result
  toolUse: [
    {
      type: 'user',
      message: { role: 'user', content: 'Run ls' },
    } satisfies StreamJsonEvent,
    {
      type: 'assistant',
      message: {
        id: 'msg-2',
        role: 'assistant',
        content: [
          { type: 'tool_use', id: 'tool-1', name: 'Bash', input: { command: 'ls -la' } },
        ],
        model: 'claude-sonnet-4-6',
      },
    } satisfies StreamJsonEvent,
    {
      type: 'tool_progress',
      tool_use_id: 'tool-1',
      elapsed_ms: 500,
    } satisfies StreamJsonEvent,
    {
      type: 'user',
      message: {
        role: 'user',
        content: [
          { type: 'tool_result', tool_use_id: 'tool-1', content: 'file1.txt\nfile2.txt', is_error: false },
        ],
      },
    } satisfies StreamJsonEvent,
    {
      type: 'result',
      subtype: 'success',
    } satisfies StreamJsonEvent,
  ] as StreamJsonEvent[],

  // Permission request: control_request event
  permissionRequest: [
    {
      type: 'user',
      message: { role: 'user', content: 'Delete the file' },
    } satisfies StreamJsonEvent,
    {
      type: 'control_request',
      subtype: 'can_use_tool',
      request_id: 'req-1',
      tool_name: 'Bash',
      input: { command: 'rm dangerous-file.txt' },
    } satisfies StreamJsonEvent,
  ] as StreamJsonEvent[],

  // Multiple simultaneous permission requests
  parallelPermissions: [
    {
      type: 'control_request',
      subtype: 'can_use_tool',
      request_id: 'req-1',
      tool_name: 'Bash',
      input: { command: 'rm file1.txt' },
    } satisfies StreamJsonEvent,
    {
      type: 'control_request',
      subtype: 'can_use_tool',
      request_id: 'req-2',
      tool_name: 'Bash',
      input: { command: 'rm file2.txt' },
    } satisfies StreamJsonEvent,
  ] as StreamJsonEvent[],

  // Compaction
  compaction: [
    {
      type: 'compact_boundary',
      summary: 'Context compacted: worked on fixing auth bug and refactoring session store.',
    } satisfies StreamJsonEvent,
    {
      type: 'user',
      message: { role: 'user', content: 'Continue with the tests' },
    } satisfies StreamJsonEvent,
  ] as StreamJsonEvent[],

  // Deduplication test: same sequence number appears twice
  withDuplicate: [
    {
      type: 'user',
      message: { role: 'user', content: 'Message 1' },
    } satisfies StreamJsonEvent,
    {
      type: 'user',
      message: { role: 'user', content: 'Message 1' }, // duplicate with same seq
    } satisfies StreamJsonEvent,
  ] as StreamJsonEvent[],
}

// ─── ConversationModel fixtures ───────────────────────────────────────────────

export function makeConversationModel(overrides: Partial<ConversationModel> = {}): ConversationModel {
  return {
    turns: [],
    ...overrides,
  }
}

export function makeUserTurn(text: string): Turn {
  return {
    id: 'turn-user-1',
    type: 'user',
    blocks: [{ type: 'text', text } as TextBlock],
  }
}

export function makeAssistantTurn(text: string): Turn {
  return {
    id: 'turn-assistant-1',
    type: 'assistant',
    blocks: [{ type: 'text', text } as TextBlock],
  }
}

export function makeToolUseTurn(toolUseId: string, toolName: string, input: unknown): Turn {
  return {
    id: 'turn-tool-1',
    type: 'assistant',
    blocks: [{
      type: 'tool_use',
      id: toolUseId,
      name: toolName,
      inputSummary: JSON.stringify(input).slice(0, 100),
      fullInput: input,
    } as ToolUseBlock],
  }
}

export function makePendingPermissionTurn(requestId: string, toolName: string): Turn {
  return {
    id: 'turn-permission-1',
    type: 'assistant',
    blocks: [{
      type: 'control_request',
      requestId,
      toolName,
      input: { command: 'dangerous command' },
      status: 'pending',
    } as ControlRequestBlock],
  }
}
