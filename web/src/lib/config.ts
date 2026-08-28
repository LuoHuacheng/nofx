export interface SystemConfig {
  initialized: boolean
  beta_mode?: boolean
  /** Claw402 guide surfaces (welcome onboarding, quick-setup entries). Default true; backend env CLAW402_GUIDE_ENABLED */
  claw402_guide_enabled?: boolean
  /** Claw402 paid data routing (x402 payment gateway). Default true; backend env FEATURE_CLAW402_ENABLED */
  claw402_enabled?: boolean
  /** Vergex signal data source. Default true; backend env FEATURE_VERGEX_ENABLED. Requires claw402_enabled. */
  vergex_enabled?: boolean
}

export function isClaw402GuideEnabled(
  config: SystemConfig | null | undefined
): boolean {
  return config?.claw402_guide_enabled !== false
}

/** Whether paid data requests are routed through claw402 x402. */
export function isClaw402Enabled(
  config: SystemConfig | null | undefined
): boolean {
  return config?.claw402_enabled !== false
}

/** Whether Vergex signal UI should be shown (requires both flags). */
export function isVergexUIEnabled(
  config: SystemConfig | null | undefined
): boolean {
  return isClaw402Enabled(config) && config?.vergex_enabled !== false
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
