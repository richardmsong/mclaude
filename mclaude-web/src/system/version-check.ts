export interface VersionCheckResult {
  blocked: boolean
  reason: 'ok' | 'below_minimum' | 'reload_pending'
  currentVersion: string
  minVersion: string
  message?: string
}

export function compareVersions(a: string, b: string): number {
  // Returns negative if a < b, 0 if equal, positive if a > b
  const partsA = a.split('.').map(Number)
  const partsB = b.split('.').map(Number)
  const len = Math.max(partsA.length, partsB.length)
  for (let i = 0; i < len; i++) {
    const diff = (partsA[i] ?? 0) - (partsB[i] ?? 0)
    if (diff !== 0) return diff
  }
  return 0
}

export function checkClientVersion(
  currentVersion: string,
  minClientVersion: string,
  reloadCount = 0,
): VersionCheckResult {
  const isBelow = compareVersions(currentVersion, minClientVersion) < 0

  if (!isBelow) {
    return { blocked: false, reason: 'ok', currentVersion, minVersion: minClientVersion }
  }

  // Below minimum
  if (reloadCount === 0) {
    // First time: will attempt reload
    return {
      blocked: true,
      reason: 'below_minimum',
      currentVersion,
      minVersion: minClientVersion,
      message: 'Updating mclaude...',
    }
  }

  // Already reloaded — show blocking screen
  return {
    blocked: true,
    reason: 'reload_pending',
    currentVersion,
    minVersion: minClientVersion,
    message: 'Server is updating, please wait...',
  }
}
