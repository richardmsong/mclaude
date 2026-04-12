import type { SessionStore } from '@/stores/session-store'
import type { ConversationVM } from './conversation-vm'

export class SkillsPickerVM {
  constructor(
    private readonly sessionStore: SessionStore,
    private readonly conversationVM: ConversationVM,
    private readonly sessionId: string,
  ) {}

  get skills(): string[] {
    const session = this.sessionStore.sessions.get(this.sessionId)
    return session?.capabilities.skills ?? []
  }

  invoke(skillName: string, args?: string): void {
    this.conversationVM.invokeSkill(skillName, args)
  }

  refresh(): void {
    this.conversationVM.reloadPlugins()
  }
}
