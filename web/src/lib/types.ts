/**
 * Mirrors the response structs in internal/api. Field names are the JSON tags,
 * not the Go identifiers. Anything marked `omitempty` or held as a pointer on
 * the Go side is optional here.
 */

/** internal/api/handlers.go — ErrorResponse */
export interface ApiErrorBody {
  error: string
}

/** internal/api/handlers.go — HealthResponse */
export interface Health {
  status: string
  time: string
}

/** internal/api/auth_handlers.go — WhoamiResponse.
 *  `token_prefix` is overloaded: it carries the token prefix for a bearer
 *  caller, the email for a cookie session, and `uid=…pid=…` over the socket.
 *  `email` is only set when the principal is a signed-in human. */
export interface Whoami {
  token_prefix: string
  scopes: string[]
  email?: string
}

/** internal/api/session_handlers.go — SessionResponse */
export interface Session {
  email: string
  expires_at: string
}

/** internal/api/handlers.go — ProjectResponse */
export interface Project {
  name: string
  created_at: string
}

/** internal/api/handlers.go — DatabaseInfoResponse. No secret. */
export interface DatabaseInfo {
  project: string
  env: string
  pr_number?: number
  database_name: string
  user_name: string
  host: string
  port: number
  created_at: string
  expires_at?: string
}

/** internal/api/handlers.go — DatabaseResponse. Returned only by create,
 *  credentials and rotate. Kept as a distinct type so the compiler stops us
 *  rendering a password we never fetched. */
export interface DatabaseSecret extends DatabaseInfo {
  password: string
  connection_string: string
}

export interface CreateDatabaseRequest {
  env: string
  pr_number?: number
  extensions?: string[]
}

/** internal/api/auth_handlers.go — TokenResponse */
export interface Token {
  name: string
  token_prefix: string
  scopes: string[]
  created_at: string
  expires_at?: string
  last_used_at?: string
  created_by?: string
  revoked_at?: string
}

export interface CreateTokenRequest {
  name: string
  scopes: string[]
  /** "90d", "24h", "1w". Empty means never. */
  expires?: string
}

/** internal/api/auth_handlers.go — CreateTokenResponse */
export interface CreatedToken {
  token: string
  token_prefix: string
  info: Token
}

/** internal/api/device_handlers.go — DeviceRequestResponse */
export interface DeviceRequest {
  user_code: string
  client_name?: string
  client_ip?: string
  requested_scopes?: string[]
  status: 'pending' | 'approved' | 'denied'
  created_at: string
  expires_at: string
  approved_by?: string
}

export interface ApproveDeviceRequest {
  name: string
  scopes: string[]
  expires?: string
}

/** internal/db/explore.go — Table */
export interface TableRef {
  schema: string
  name: string
}

/** internal/db/explore.go — Column */
export interface Column {
  name: string
  type: string
  nullable: boolean
  default?: string
  primary_key: boolean
}

/** internal/db/explore.go — Row */
export type Row = Record<string, unknown>

/** internal/db/explore.go — RowPage */
export interface RowPage {
  columns: Column[]
  rows: Row[]
  total: number
  limit: number
  offset: number
}

export interface RowMutation {
  key?: Row
  values?: Row
}

/** internal/api/handlers.go — CleanupResponse */
export interface CleanupResult {
  deleted: string[]
  count: number
}

/** Environments the server accepts (internal/project). */
export const ENVIRONMENTS = ['dev', 'staging', 'prod', 'pr'] as const
export type Environment = (typeof ENVIRONMENTS)[number]

/** internal/db/explore.go — MaxRowLimit. The server silently caps above this. */
export const MAX_ROW_LIMIT = 200
