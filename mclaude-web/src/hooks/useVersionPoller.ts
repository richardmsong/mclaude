import { useEffect, useState } from 'react'
import { checkClientVersion } from '@/system/version-check'

// CLIENT_VERSION is the version of this SPA build.
// In production it is injected by Vite via define. In development it falls back to '0.0.0'.
declare const __CLIENT_VERSION__: string | undefined
const CLIENT_VERSION: string =
  typeof __CLIENT_VERSION__ !== 'undefined' ? __CLIENT_VERSION__ : '0.0.0'

// Extract the bundle hash from index.html text.
// Matches <script ... src="/assets/index-HASH.js">
function extractBundleHash(html: string): string | null {
  const m = /\/assets\/index-([^.]+)\.js/.exec(html)
  return m ? m[1]! : null
}

// Read the currently-loaded bundle hash from the DOM.
function currentBundleHash(): string | null {
  const scripts = document.querySelectorAll<HTMLScriptElement>('script[src]')
  for (const s of scripts) {
    const m = /\/assets\/index-([^.]+)\.js/.exec(s.src)
    if (m) return m[1]!
  }
  return null
}

// TODO: wire to /api/version endpoint once control-plane is confirmed reachable from SPA.
// Currently calls GET /version (no auth required) and returns minClientVersion or null on error.
async function fetchMinClientVersion(): Promise<string | null> {
  try {
    const res = await fetch(window.location.origin + '/version', { cache: 'no-store' })
    if (!res.ok) return null
    const json = await res.json() as { minClientVersion?: string }
    return typeof json.minClientVersion === 'string' ? json.minClientVersion : null
  } catch {
    return null
  }
}

// Track how many times we have reloaded in this session (persisted via sessionStorage
// so a reload resets it to 0 on the new page load, which is intentional).
function getReloadCount(): number {
  try {
    return parseInt(sessionStorage.getItem('mclaude.versionReloadCount') ?? '0', 10)
  } catch {
    return 0
  }
}

function incrementReloadCount(): void {
  try {
    sessionStorage.setItem('mclaude.versionReloadCount', String(getReloadCount() + 1))
  } catch {}
}

export interface VersionPollerResult {
  updateAvailable: boolean
  /** True when the client is below minClientVersion and cannot continue. */
  blocked: boolean
  /** Human-readable reason when blocked is true. */
  blockMessage?: string
}

export function useVersionPoller(intervalMs = 60_000): VersionPollerResult {
  const [updateAvailable, setUpdateAvailable] = useState(false)
  const [blocked, setBlocked] = useState(false)
  const [blockMessage, setBlockMessage] = useState<string | undefined>()

  useEffect(() => {
    const loaded = currentBundleHash()
    let cancelled = false

    const check = async () => {
      if (cancelled) return
      try {
        // --- Bundle hash check (update available banner) ---
        const res = await fetch(window.location.origin + '/', { cache: 'no-store' })
        if (!res.ok || cancelled) return
        const html = await res.text()
        if (loaded) {
          const remote = extractBundleHash(html)
          if (remote && remote !== loaded) {
            setUpdateAvailable(true)
          }
        }

        // --- X4: minClientVersion check ---
        const minVersion = await fetchMinClientVersion()
        if (cancelled || minVersion === null) return

        const reloadCount = getReloadCount()
        const result = checkClientVersion(CLIENT_VERSION, minVersion, reloadCount)

        if (!result.blocked) return

        if (result.reason === 'below_minimum' && reloadCount === 0) {
          // First occurrence: attempt a hard reload to pick up new assets.
          incrementReloadCount()
          window.location.reload()
          return
        }

        // Still blocked after reload — show blocking screen.
        setBlocked(true)
        setBlockMessage(result.message)
      } catch {
        // network error — ignore silently
      }
    }

    let intervalId: ReturnType<typeof setInterval> | undefined

    // Delay first poll 30 s to avoid hitting the server on initial render.
    const firstTimerId = setTimeout(() => {
      check()
      intervalId = setInterval(check, intervalMs)
    }, 30_000)

    return () => {
      cancelled = true
      clearTimeout(firstTimerId)
      if (intervalId !== undefined) clearInterval(intervalId)
    }
  }, [intervalMs])

  return { updateAvailable, blocked, blockMessage }
}
