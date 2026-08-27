export interface SystemConfig {
  initialized: boolean
  beta_mode?: boolean
  /** Claw402 guide surfaces (welcome onboarding, quick-setup entries). Default true; backend env CLAW402_GUIDE_ENABLED */
  claw402_guide_enabled?: boolean
}

export function isClaw402GuideEnabled(
  config: SystemConfig | null | undefined
): boolean {
  return config?.claw402_guide_enabled !== false
}

let configPromise: Promise<SystemConfig> | null = null
let cachedConfig: SystemConfig | null = null

export function getSystemConfig(): Promise<SystemConfig> {
  if (cachedConfig) {
    return Promise.resolve(cachedConfig)
  }
  if (configPromise) {
    return configPromise
  }
  configPromise = fetch('/api/config')
    .then((res) => res.json())
    .then((data: SystemConfig) => {
      cachedConfig = data
      return data
    })
  return configPromise
}

/** Sync read of the last fetched config; null before first fetch. */
export function getCachedSystemConfig(): SystemConfig | null {
  return cachedConfig
}

/** Call after first-time setup completes so next check reflects initialized=true */
export function invalidateSystemConfig() {
  cachedConfig = null
  configPromise = null
  window.dispatchEvent(new Event('system-config-invalidated'))
}
