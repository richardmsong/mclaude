# BUG-002: Push-to-Talk only works ~1 in 10 attempts

**Severity**: High — core interaction feature unreliable  
**Component**: spa (SessionDetailScreen.tsx)  
**Reported**: 2026-04-16  

## Symptoms

- User presses and holds the PTT button to record
- Release triggers transcription and send
- Subsequent presses fail silently — no recording starts
- Works again intermittently (~1 in 10 tries)

## Root Cause

Race condition between `handlePttStop()`, the browser's SpeechRecognition `onend` event, and React state updates.

**Sequence that causes the bug:**

1. User releases button → `handlePttStop()` fires
2. `handlePttStop()` calls `recognition.stop()`, immediately sets `pttRecognitionRef.current = null`
3. Browser fires `onend` event asynchronously
4. `onend` handler sets `setPttRecording(false)` (React async state update)
5. User presses button again before step 4 completes
6. `handlePttStart()` checks `if (pttRecording)` — still `true` from previous attempt → exits early
7. Recording never starts

**Additional failure mode:** If the user presses quickly enough, `handlePttStart()` creates a NEW SpeechRecognition instance, but the OLD instance's `onend` fires and sets `pttRecording = false`, causing the new recording to think it was stopped.

## Evidence

Code at `mclaude-web/src/components/SessionDetailScreen.tsx`:
- `handlePttStart()`: lines 284-347 — checks `pttRecording` guard at line 293
- `handlePttStop()`: lines 349-355 — clears ref and state
- `onend` handler: lines 334-342 — also clears ref and state (duplicate cleanup)

The guard at line 293 (`if (pttRecording)`) uses React state which is async. The ref (`pttRecognitionRef.current`) is cleared synchronously in `handlePttStop()` but the state lags behind.

## Fix

1. Use the ref as the source of truth for guard checks, not React state (`if (pttRecognitionRef.current)` instead of `if (pttRecording)`)
2. Remove duplicate cleanup in `onend` — let `handlePttStop()` be the single owner of cleanup
3. Add a guard in `onend` to check if the recognition instance matches the current ref before clearing state (prevents old instance from interfering with new one)
4. Consider using a single `recognitionIdRef` counter to invalidate stale callbacks

## Files

- `mclaude-web/src/components/SessionDetailScreen.tsx:284-355` — PTT recording logic
