# BUG-003: First-run message can inject at wrong position or duplicate

**Severity**: Medium — broken first-run experience  
**Component**: spa (App.tsx, SessionDetailScreen.tsx, event-store.ts)  
**Reported**: 2026-04-16  

## Symptoms

- User opens MClaude for the first time (no existing sessions)
- "Getting Started" session is created and an onboarding message is auto-sent
- The message may appear duplicated (once as pending, once as confirmed turn)
- Or the message appears but the echo event is lost (pending message stays in "sending..." state)

## Root Cause

Race condition between EventStore JetStream subscription (async) and the initial message send (fixed 500ms timer).

**Timeline:**

1. `App.tsx:171-198` — 1000ms after mount, creates session, sets `initialMessage` state, navigates to session
2. `App.tsx:228-254` — creates EventStore, calls `store.start(replayFromSeq)` which begins JetStream subscription (async)
3. `SessionDetailScreen.tsx:114-122` — receives `initialMessage` prop, starts 500ms timer
4. Timer fires, calls `conversationVM.sendMessage(initialMessage)` which:
   - Adds to `_pendingMessages` array immediately (event-store.ts:171)
   - Publishes to NATS (conversation-vm.ts:61)
5. Server processes message, publishes `user` event back to stream
6. **If JetStream subscription from step 2 hasn't resolved yet**: the echo event arrives but `_applyEvent()` handler is not active. Pending message never gets removed.

**No synchronization exists** between "EventStore subscription is ready" and "send initial message." The 500ms delay is arbitrary and insufficient.

## Evidence

- `event-store.ts:115-121` — `jsSubscribe()` is async, resolves in `.then()` callback
- `SessionDetailScreen.tsx:120` — fixed `setTimeout(500)` with no readiness check
- `EventList.tsx:97-162` — renders both `turns` and `pendingMessages` separately with no dedup guard

## Fix

1. Add a readiness signal to EventStore (e.g., a Promise that resolves when JetStream subscription is active)
2. `SessionDetailScreen` should await EventStore readiness before sending the initial message, instead of using a fixed 500ms timer
3. Add dedup guard in EventList: if a turn with matching UUID exists, don't render the pending message

## Files

- `mclaude-web/src/components/App.tsx:171-198, 228-254` — first-run flow and EventStore creation
- `mclaude-web/src/components/SessionDetailScreen.tsx:114-122` — initial message send timer
- `mclaude-web/src/stores/event-store.ts:68-121, 171-179, 389-395` — subscription and pending message handling
- `mclaude-web/src/components/events/EventList.tsx:97-162` — rendering logic
