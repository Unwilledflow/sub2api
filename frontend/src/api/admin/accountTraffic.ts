import { apiClient } from '../client'

/**
 * Adaptive concurrency exposes only its on/off switch and its mode. The floor
 * and the back-off timing are fixed by the backend so operators do not have to
 * balance six interacting numbers per account.
 */
export interface AccountTrafficPolicy {
  strict_rpm_enabled: boolean; rpm: number; burst: number
  adaptive_enabled: boolean; adaptive_mode: 'observe' | 'automatic'
  min_concurrency: number; failure_threshold: number; failure_window_seconds: number; recovery_seconds: number
}
/** Fixed by the backend; kept here so drafts and payloads stay well-formed. */
export const trafficFixedTuning = { min_concurrency: 50, failure_threshold: 3, failure_window_seconds: 60, recovery_seconds: 60 } as const
export interface AccountTrafficState {
  effective_concurrency: number; recommended_concurrency: number; in_flight: number; requests_last_minute: number
  accepted: number; rejected_rpm: number; rejected_concurrency: number; upstream_429: number; upstream_5xx: number
  completed: number; average_duration_ms: number; last_adjustment_at?: string
}
export interface AccountTrafficResponse { policy: AccountTrafficPolicy; state?: AccountTrafficState | null; state_available: boolean; hard_limit: number }
export const defaultTrafficPolicy = (): AccountTrafficPolicy => ({
  strict_rpm_enabled: false,
  rpm: 60,
  burst: 5,
  adaptive_enabled: true,
  adaptive_mode: 'automatic',
  ...trafficFixedTuning,
})
const integerIn = (value: number, min: number, max: number) => Number.isInteger(value) && value >= min && value <= max
export function normalizeTrafficDraft(value: AccountTrafficPolicy): AccountTrafficPolicy {
  const policy = { ...value, ...trafficFixedTuning }, defaults = defaultTrafficPolicy()
  if (!policy.strict_rpm_enabled) {
    if (!integerIn(policy.rpm, 1, 60000)) policy.rpm = defaults.rpm
    if (!integerIn(policy.burst, 1, policy.rpm)) policy.burst = Math.min(defaults.burst, policy.rpm)
  }
  return policy
}
export function trafficPolicyError(policy: AccountTrafficPolicy): string {
  if (policy.strict_rpm_enabled && (!integerIn(policy.rpm, 1, 60000) || !integerIn(policy.burst, 1, policy.rpm))) return 'RPM 必须为 1–60000 的整数，突发额度必须为正整数，突发额度不能大于 RPM'
  if (policy.adaptive_enabled && !['observe', 'automatic'].includes(policy.adaptive_mode)) return '请选择有效的运行方式'
  return ''
}
export const accountTrafficAPI = {
  async get(id: number, signal?: AbortSignal) { return (await apiClient.get<AccountTrafficResponse>(`/admin/accounts/${id}/traffic-control`, { signal })).data },
  async save(id: number, policy: AccountTrafficPolicy) { return (await apiClient.put<AccountTrafficResponse>(`/admin/accounts/${id}/traffic-control`, policy)).data }
}
