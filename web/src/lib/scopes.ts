import type { Whoami } from './types'

/**
 * Mirrors internal/auth/token.go. Used only to decide what to *show* — the
 * server re-checks every request, so a wrong answer here is a UX bug, not a
 * security hole. Kept honest by src/lib/scopes.test.ts.
 */

export const SCOPE_ADMIN = 'admin'
export const SCOPE_TOKENS = 'tokens'

export interface ScopeRequest {
  resource: 'project' | 'token'
  project?: string
  env?: string
}

/** internal/auth/token.go — scopeAllows */
export function scopeAllows(scope: string, req: ScopeRequest): boolean {
  if (scope === SCOPE_ADMIN) return true
  if (scope === SCOPE_TOKENS) return req.resource === 'token'
  if (req.resource !== 'project') return false

  const parts = scope.split(':')
  if (parts[0] !== 'project') return false

  if (parts.length === 2) {
    return parts[1] === '*' || parts[1] === req.project
  }
  if (parts.length === 4) {
    if (parts[1] !== '*' && parts[1] !== req.project) return false
    if (parts[2] === 'pr') {
      // The server does not scope per-PR-number; only `pr:*` exists.
      return req.env === 'pr' && parts[3] === '*'
    }
    if (parts[2] === 'env') return parts[3] === req.env
  }
  return false
}

export function authorize(held: string[] | undefined, req: ScopeRequest): boolean {
  return (held ?? []).some((s) => scopeAllows(s, req))
}

/** Can this principal see the Tokens and Devices sections? */
export function canManageTokens(who: Whoami | undefined): boolean {
  return authorize(who?.scopes, { resource: 'token' })
}

/** `admin` is the only scope that satisfies cleanup, which is fleet-wide. */
export function isAdmin(who: Whoami | undefined): boolean {
  return (who?.scopes ?? []).includes(SCOPE_ADMIN)
}

/** Only a signed-in human has a password to change; bearer tokens do not. */
export function isHuman(who: Whoami | undefined): boolean {
  return Boolean(who?.email)
}

/** What to show as "you" in the header. */
export function principalLabel(who: Whoami | undefined): string {
  return who?.email || who?.token_prefix || 'unknown'
}

/** internal/auth/token.go — ValidateScopes. Returns null when valid. */
export function validateScope(scope: string): string | null {
  if (scope === SCOPE_ADMIN || scope === SCOPE_TOKENS) return null

  const parts = scope.split(':')
  if (parts[0] !== 'project') return `Invalid scope "${scope}"`

  if (parts.length === 2) {
    return parts[1] ? null : `Invalid scope "${scope}"`
  }
  if (parts.length === 4) {
    if (!parts[1]) return `Invalid scope "${scope}"`
    if (parts[2] === 'env') {
      return ['prod', 'dev', 'staging', 'pr'].includes(parts[3])
        ? null
        : `Invalid env in scope "${scope}"`
    }
    if (parts[2] === 'pr') {
      return parts[3] === '*' ? null : `Scope "${scope}": pr scope only supports '*'`
    }
    return `Invalid scope "${scope}"`
  }
  return `Invalid scope "${scope}"`
}

export function validateScopes(scopes: string[]): string | null {
  if (scopes.length === 0) return 'At least one scope is required'
  for (const s of scopes) {
    const err = validateScope(s)
    if (err) return err
  }
  return null
}

/** Rough blast-radius ordering, used to tint scope badges. */
export function scopeSeverity(scope: string): 'high' | 'medium' | 'low' {
  if (scope === SCOPE_ADMIN) return 'high'
  if (scope === SCOPE_TOKENS) return 'high'
  if (scope === 'project:*') return 'high'
  const parts = scope.split(':')
  if (parts.length === 2) return 'medium'
  if (parts[2] === 'env' && parts[3] === 'prod') return 'medium'
  return 'low'
}
