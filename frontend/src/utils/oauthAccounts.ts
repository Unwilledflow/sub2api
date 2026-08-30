export interface OAuthAccountValidityFields {
  platform: string
  type: string
  status: string
  error_message?: string | null
  credentials?: Record<string, unknown> | null
  credentials_status?: Record<string, boolean>
}

export function isPermanentlyInvalidOpenAIOAuthAccount(
  account: OAuthAccountValidityFields
): boolean {
  if (account.platform !== 'openai' || account.type !== 'oauth') return false

  const credentialStatus = account.credentials_status
  if (credentialStatus || account.credentials !== undefined) {
    const tokenKeys = ['has_access_token', 'has_refresh_token']
    const hasAnyToken = tokenKeys.some(key => credentialStatus?.[key] === true)
    if (!hasAnyToken) return true
  }

  if (account.status !== 'error') return false
  const message = String(account.error_message ?? '').trim().toLowerCase()
  if (!message) return false

  const transientMarkers = [
    '429',
    'rate limit',
    'too many requests',
    'bad gateway',
    'gateway timeout',
    'upstream 5',
    'http 5',
    'timeout',
    'connection reset',
    'connection refused',
    'temporarily unavailable',
    'high demand',
    'cyber_policy',
    'policy',
    'overloaded'
  ]
  if (transientMarkers.some(marker => message.includes(marker))) return false

  const permanentMarkers = [
    'authentication failed (401)',
    'chat completions authentication failed (401)',
    'http 401',
    'status 401',
    'invalid_grant',
    'invalid token',
    'invalid access token',
    'invalid refresh token',
    'refresh token invalid',
    'refresh token expired',
    'refresh token revoked',
    'token has been revoked',
    'token revoked',
    'access_token expired and refresh_token is missing',
    'access token expired and refresh token is missing',
    'account deactivated',
    'account disabled',
    'workspace deactivated',
    'workspace has been deactivated'
  ]

  return permanentMarkers.some(marker => message.includes(marker))
}
