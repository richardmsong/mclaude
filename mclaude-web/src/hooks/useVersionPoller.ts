import { useEffect, useState } from 'react'

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

export function useVersionPoller(intervalMs = 60_000): { updateAvailable: boolean } {
  const [updateAvailable, setUpdateAvailable] = useState(false)

  useEffect(() => {
    const loaded = currentBundleHash()
    if (!loaded) return // can't determine current hash — skip polling

    let cancelled = false

    const check = async () => {
      if (cancelled) return
      try {
        const res = await fetch(window.location.origin + '/', { cache: 'no-store' })
        if (!res.ok || cancelled) return
        const html = await res.text()
        const remote = extractBundleHash(html)
        if (remote && remote !== loaded) {
          setUpdateAvailable(true)
        }
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

  return { updateAvailable }
}
