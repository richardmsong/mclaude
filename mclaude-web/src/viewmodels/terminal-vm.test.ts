import { describe, it, expect, beforeEach } from 'vitest'
import { TerminalVM } from './terminal-vm'
import { MockNATSClient } from '../testutil/mock-nats'

const enc = new TextEncoder()
const dec = new TextDecoder()

function parsePublished(data: Uint8Array): unknown {
  return JSON.parse(dec.decode(data))
}

describe('TerminalVM', () => {
  let mockNats: MockNATSClient
  let vm: TerminalVM

  beforeEach(() => {
    mockNats = new MockNATSClient()
    vm = new TerminalVM(mockNats, 'user-1', 'project-1')
  })

  describe('createTerminal', () => {
    it('requests terminal creation via api.terminal.create and returns terminalId', async () => {
      mockNats.requestHandlers.set(
        'mclaude.users.user-1.projects.project-1.api.terminal.create',
        () => enc.encode(JSON.stringify({ terminalId: 'term-1' })),
      )

      const id = await vm.createTerminal('/home/user')
      expect(id).toBe('term-1')
      expect(vm.terminals).toHaveLength(1)
      expect(vm.terminals[0]!.id).toBe('term-1')
      expect(vm.terminals[0]!.cwd).toBe('/home/user')
    })

    it('publishes to mclaude.{userId}.{projectId}.api.terminal.create with cwd', async () => {
      mockNats.requestHandlers.set(
        'mclaude.users.user-1.projects.project-1.api.terminal.create',
        () => enc.encode(JSON.stringify({ terminalId: 'term-2' })),
      )

      await vm.createTerminal('/tmp')
      const req = mockNats.requests.find(r => r.subject === 'mclaude.users.user-1.projects.project-1.api.terminal.create')
      expect(req).toBeDefined()
      expect(parsePublished(req!.data)).toMatchObject({ cwd: '/tmp' })
    })

    it('subscribes to terminal output after creation', async () => {
      mockNats.requestHandlers.set(
        'mclaude.users.user-1.projects.project-1.api.terminal.create',
        () => enc.encode(JSON.stringify({ terminalId: 'term-3' })),
      )

      const id = await vm.createTerminal()
      const received: Uint8Array[] = []
      vm.onOutput(id, data => received.push(data))

      const outputSubject = `mclaude.users.user-1.projects.project-1.api.terminal.${id}.output`
      mockNats.simulateReceive(outputSubject, 'hello from pty')
      expect(received).toHaveLength(1)
    })
  })

  describe('deleteTerminal', () => {
    it('requests terminal deletion via api.terminal.delete', async () => {
      mockNats.requestHandlers.set(
        'mclaude.users.user-1.projects.project-1.api.terminal.create',
        () => enc.encode(JSON.stringify({ terminalId: 'term-del' })),
      )
      const id = await vm.createTerminal()
      expect(vm.terminals).toHaveLength(1)

      await vm.deleteTerminal(id)

      expect(vm.terminals).toHaveLength(0)
      const req = mockNats.requests.find(r => r.subject === 'mclaude.users.user-1.projects.project-1.api.terminal.delete')
      expect(req).toBeDefined()
      expect(parsePublished(req!.data)).toMatchObject({ terminalId: id })
    })
  })

  describe('sendInput', () => {
    it('publishes raw bytes to terminal.{termId}.input', () => {
      const data = enc.encode('\x04') // Ctrl+D
      vm.sendInput('term-1', data)

      const pub = mockNats.published.find(p => p.subject === 'mclaude.users.user-1.projects.project-1.api.terminal.term-1.input')
      expect(pub).toBeDefined()
      expect(pub!.data).toEqual(data)
    })
  })

  describe('resize', () => {
    it('publishes resize event to api.terminal.resize', () => {
      vm.resize('term-1', 24, 80)

      const pub = mockNats.published.find(p => p.subject === 'mclaude.users.user-1.projects.project-1.api.terminal.resize')
      expect(pub).toBeDefined()
      expect(parsePublished(pub!.data)).toMatchObject({ terminalId: 'term-1', rows: 24, cols: 80 })
    })
  })

  describe('onOutput', () => {
    it('delivers output to all registered listeners for that terminal', async () => {
      mockNats.requestHandlers.set(
        'mclaude.users.user-1.projects.project-1.api.terminal.create',
        () => enc.encode(JSON.stringify({ terminalId: 'term-out' })),
      )
      const id = await vm.createTerminal()

      const received1: Uint8Array[] = []
      const received2: Uint8Array[] = []
      vm.onOutput(id, d => received1.push(d))
      vm.onOutput(id, d => received2.push(d))

      mockNats.simulateReceive(`mclaude.users.user-1.projects.project-1.api.terminal.${id}.output`, 'data')
      expect(received1).toHaveLength(1)
      expect(received2).toHaveLength(1)
    })

    it('unsubscribe stops listener from receiving output', async () => {
      mockNats.requestHandlers.set(
        'mclaude.users.user-1.projects.project-1.api.terminal.create',
        () => enc.encode(JSON.stringify({ terminalId: 'term-unsub' })),
      )
      const id = await vm.createTerminal()

      const received: Uint8Array[] = []
      const unsub = vm.onOutput(id, d => received.push(d))
      unsub()

      mockNats.simulateReceive(`mclaude.users.user-1.projects.project-1.api.terminal.${id}.output`, 'data')
      expect(received).toHaveLength(0)
    })
  })
})
