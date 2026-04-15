## Run: 2026-04-14T00:00:00Z

GAP: "Sessions in 'updating' state are resumed but their KV entry is NOT updated yet (the 'updating' banner stays visible in the UI until clearUpdatingState() runs). [...] The clearUpdatingState() in step 4 is what actually writes state:'idle' to KV for these sessions — only after consumers are attached and the agent is ready to process messages" → In `recoverSessions()`, `clearPendingControlsForResume(&st)` sets `st.State = StateIdle` before creating the session object (`newSession(st, a.userID)`). So the session's in-memory state is `StateIdle` from the start. When `clearUpdatingState()` subsequently runs and calls `sess.getState().State`, it finds `StateIdle` (not `StateUpdating`) and skips the KV write entirely. The KV entry for an "updating" session is never updated to idle — the UI banner never clears. (agent.go:198-213, agent.go:384-398)

## Run: 2026-04-14T12:00:00Z (re-audit after clearUpdatingState fix)

CLEAN
