import { describe, expect, it } from 'vitest'
import {
  authorize,
  canManageTokens,
  isAdmin,
  isHuman,
  principalLabel,
  scopeAllows,
  validateScope,
  validateScopes,
} from './scopes'
import type { Whoami } from './types'

/**
 * These are the literal scope strings internal/auth accepts. If a Go-side
 * rename lands, this file should go red rather than the nav silently hiding
 * sections the principal can actually use.
 */

describe('scopeAllows', () => {
  it('admin satisfies everything', () => {
    expect(scopeAllows('admin', { resource: 'token' })).toBe(true)
    expect(scopeAllows('admin', { resource: 'project', project: 'x', env: 'prod' })).toBe(true)
  })

  it('tokens satisfies only token management', () => {
    expect(scopeAllows('tokens', { resource: 'token' })).toBe(true)
    expect(scopeAllows('tokens', { resource: 'project', project: 'x' })).toBe(false)
  })

  it('project:* matches any project', () => {
    expect(scopeAllows('project:*', { resource: 'project', project: 'anything' })).toBe(true)
    // ...but is not token management.
    expect(scopeAllows('project:*', { resource: 'token' })).toBe(false)
  })

  it('project:<name> matches only that project', () => {
    expect(scopeAllows('project:myapp', { resource: 'project', project: 'myapp' })).toBe(true)
    expect(scopeAllows('project:myapp', { resource: 'project', project: 'other' })).toBe(false)
  })

  it('project:<name>:env:<env> matches only that env', () => {
    const s = 'project:myapp:env:dev'
    expect(scopeAllows(s, { resource: 'project', project: 'myapp', env: 'dev' })).toBe(true)
    expect(scopeAllows(s, { resource: 'project', project: 'myapp', env: 'prod' })).toBe(false)
    expect(scopeAllows(s, { resource: 'project', project: 'other', env: 'dev' })).toBe(false)
  })

  it('project:<name>:pr:* matches only PR databases', () => {
    const s = 'project:myapp:pr:*'
    expect(scopeAllows(s, { resource: 'project', project: 'myapp', env: 'pr' })).toBe(true)
    expect(scopeAllows(s, { resource: 'project', project: 'myapp', env: 'dev' })).toBe(false)
  })

  it('rejects malformed shapes', () => {
    expect(scopeAllows('project', { resource: 'project', project: 'x' })).toBe(false)
    expect(scopeAllows('nonsense', { resource: 'project', project: 'x' })).toBe(false)
    expect(scopeAllows('project:x:env', { resource: 'project', project: 'x' })).toBe(false)
  })
})

describe('authorize', () => {
  it('is satisfied by any one held scope', () => {
    expect(
      authorize(['project:a', 'project:b'], { resource: 'project', project: 'b' }),
    ).toBe(true)
    expect(authorize([], { resource: 'project', project: 'b' })).toBe(false)
    expect(authorize(undefined, { resource: 'token' })).toBe(false)
  })
})

describe('principal helpers', () => {
  const human: Whoami = { token_prefix: 'me@example.com', scopes: ['admin'], email: 'me@example.com' }
  const ci: Whoami = { token_prefix: 'pgm_live_abcd', scopes: ['project:myapp:pr:*'] }

  it('shows Tokens and Devices only to a principal that can manage tokens', () => {
    expect(canManageTokens(human)).toBe(true)
    expect(canManageTokens({ token_prefix: 'x', scopes: ['tokens'] })).toBe(true)
    // The bug this guards: a project-scoped token used to be signed straight
    // out when it hit /auth/tokens.
    expect(canManageTokens(ci)).toBe(false)
    expect(canManageTokens(undefined)).toBe(false)
  })

  it('reserves cleanup for admin', () => {
    expect(isAdmin(human)).toBe(true)
    expect(isAdmin({ token_prefix: 'x', scopes: ['project:*'] })).toBe(false)
  })

  it('only a signed-in human has a password to change', () => {
    expect(isHuman(human)).toBe(true)
    expect(isHuman(ci)).toBe(false)
  })

  it('labels a principal by email when there is one, prefix otherwise', () => {
    expect(principalLabel(human)).toBe('me@example.com')
    expect(principalLabel(ci)).toBe('pgm_live_abcd')
    expect(principalLabel(undefined)).toBe('unknown')
  })
})

describe('validateScope', () => {
  it('accepts every documented shape', () => {
    for (const s of [
      'admin',
      'tokens',
      'project:*',
      'project:myapp',
      'project:myapp:env:prod',
      'project:myapp:env:dev',
      'project:myapp:env:staging',
      'project:myapp:env:pr',
      'project:myapp:pr:*',
    ]) {
      expect(validateScope(s), s).toBeNull()
    }
  })

  it('rejects what the server rejects', () => {
    expect(validateScope('project')).toBeTruthy()
    expect(validateScope('project:')).toBeTruthy()
    expect(validateScope('bogus')).toBeTruthy()
    expect(validateScope('project:myapp:env:qa')).toBeTruthy()
    // The server does not scope per-PR-number.
    expect(validateScope('project:myapp:pr:42')).toBeTruthy()
    expect(validateScope('project:myapp:thing:x')).toBeTruthy()
  })

  it('requires at least one scope', () => {
    expect(validateScopes([])).toBeTruthy()
    expect(validateScopes(['admin'])).toBeNull()
    expect(validateScopes(['admin', 'bogus'])).toBeTruthy()
  })
})
