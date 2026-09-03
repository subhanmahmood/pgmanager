import type {
  ApproveDeviceRequest,
  CleanupResult,
  CreateDatabaseRequest,
  CreateTokenRequest,
  CreatedToken,
  DatabaseInfo,
  DatabaseSecret,
  DeviceRequest,
  Health,
  Project,
  Row,
  RowMutation,
  RowPage,
  Session,
  TableRef,
  Token,
  Whoami,
} from './types'

const BASE = '/api'

export class ApiError extends Error {
  /** 0 means the request never reached the server. */
  readonly status: number

  constructor(message: string, status: number) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  let res: Response
  try {
    res = await fetch(BASE + path, {
      method,
      credentials: 'same-origin',
      headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  } catch {
    throw new ApiError('Cannot reach the pgmanager server.', 0)
  }

  if (res.status === 204) return null as T

  const text = await res.text()
  let data: unknown = null
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      /* non-JSON body — fall back to the raw text below */
    }
  }

  if (!res.ok) {
    const fromBody =
      data && typeof data === 'object' && 'error' in data
        ? String((data as { error: unknown }).error)
        : ''
    throw new ApiError(fromBody || text || `Request failed (${res.status})`, res.status)
  }
  return data as T
}

/** Path segments come from user data (project names, table names), so they are
 *  always escaped. `env` is already in its `dev` / `pr_42` wire form. */
const seg = encodeURIComponent

export const api = {
  health: () => request<Health>('GET', '/health'),

  whoami: () => request<Whoami>('GET', '/auth/whoami'),
  login: (email: string, password: string) =>
    request<Session>('POST', '/auth/login', { email, password }),
  logout: () => request<null>('POST', '/auth/logout'),
  changePassword: (current: string, next: string) =>
    request<null>('POST', '/auth/password', { current, new: next }),

  listTokens: () => request<Token[]>('GET', '/auth/tokens'),
  createToken: (body: CreateTokenRequest) =>
    request<CreatedToken>('POST', '/auth/tokens', body),
  revokeToken: (prefix: string) => request<null>('DELETE', `/auth/tokens/${seg(prefix)}`),

  listDevices: () => request<DeviceRequest[]>('GET', '/auth/devices'),
  getDevice: (code: string) => request<DeviceRequest>('GET', `/auth/device/${seg(code)}`),
  approveDevice: (code: string, body: ApproveDeviceRequest) =>
    request<Token>('POST', `/auth/device/${seg(code)}/approve`, body),
  denyDevice: (code: string) => request<null>('POST', `/auth/device/${seg(code)}/deny`),

  listProjects: () => request<Project[]>('GET', '/projects'),
  createProject: (name: string) => request<Project>('POST', '/projects', { name }),
  deleteProject: (name: string) => request<null>('DELETE', `/projects/${seg(name)}`),

  listDatabases: (project: string) =>
    request<DatabaseInfo[]>('GET', `/projects/${seg(project)}/databases`),
  createDatabase: (project: string, body: CreateDatabaseRequest) =>
    request<DatabaseSecret>('POST', `/projects/${seg(project)}/databases`, body),
  getDatabase: (project: string, env: string) =>
    request<DatabaseInfo>('GET', `/projects/${seg(project)}/databases/${seg(env)}`),
  credentials: (project: string, env: string) =>
    request<DatabaseSecret>('GET', `/projects/${seg(project)}/databases/${seg(env)}/credentials`),
  rotatePassword: (project: string, env: string, terminate: boolean) =>
    request<DatabaseSecret>('POST', `/projects/${seg(project)}/databases/${seg(env)}/rotate`, {
      terminate,
    }),
  renewDatabase: (project: string, env: string, ttl?: string) =>
    request<DatabaseInfo>('POST', `/projects/${seg(project)}/databases/${seg(env)}/renew`, {
      ttl,
    }),
  deleteDatabase: (project: string, env: string) =>
    request<null>('DELETE', `/projects/${seg(project)}/databases/${seg(env)}`),

  listTables: (project: string, env: string) =>
    request<{ tables: TableRef[] | null }>(
      'GET',
      `/projects/${seg(project)}/databases/${seg(env)}/tables`,
    ),
  listRows: (
    project: string,
    env: string,
    table: string,
    opts: { schema: string; limit: number; offset: number },
  ) => {
    const q = new URLSearchParams({
      schema: opts.schema,
      limit: String(opts.limit),
      offset: String(opts.offset),
    })
    return request<RowPage>(
      'GET',
      `/projects/${seg(project)}/databases/${seg(env)}/tables/${seg(table)}/rows?${q}`,
    )
  },
  insertRow: (project: string, env: string, table: string, schema: string, body: RowMutation) =>
    request<Row>(
      'POST',
      `/projects/${seg(project)}/databases/${seg(env)}/tables/${seg(table)}/rows?schema=${seg(schema)}`,
      body,
    ),
  updateRow: (project: string, env: string, table: string, schema: string, body: RowMutation) =>
    request<Row>(
      'PATCH',
      `/projects/${seg(project)}/databases/${seg(env)}/tables/${seg(table)}/rows?schema=${seg(schema)}`,
      body,
    ),
  deleteRow: (project: string, env: string, table: string, schema: string, body: RowMutation) =>
    request<null>(
      'DELETE',
      `/projects/${seg(project)}/databases/${seg(env)}/tables/${seg(table)}/rows?schema=${seg(schema)}`,
      body,
    ),

  cleanup: (olderThan: string) =>
    request<CleanupResult>('POST', '/cleanup', { older_than: olderThan }),
}
