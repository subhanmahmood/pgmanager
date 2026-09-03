import { isKeyedEnv } from './types'
import type { DatabaseInfo } from './types'

/** The API's URL segment for a database: "dev", "pr_42", "scratch_epic_231".
 *  Feeds both routing and every database API path, so getting it wrong breaks
 *  the whole subtree. */
export function envSegment(db: Pick<DatabaseInfo, 'env' | 'key' | 'pr_number'>): string {
  const key = db.key ?? (db.pr_number !== undefined ? String(db.pr_number) : '')
  return key ? `${db.env}_${key}` : db.env
}

/** Inverse of envSegment, for reading a route param back. Env names carry no
 *  underscore, so the first one separates the env from its key; a key may
 *  contain further underscores and keeps them. */
export function parseEnvSegment(segment: string): { env: string; key?: string } {
  const cut = segment.indexOf('_')
  if (cut === -1) return { env: segment }
  const env = segment.slice(0, cut)
  if (!isKeyedEnv(env)) return { env: segment }
  return { env, key: segment.slice(cut + 1) }
}

export function fmtDate(s?: string | null): string {
  if (!s) return '—'
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return '—'
  return d.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

export function isExpired(s?: string | null): boolean {
  return Boolean(s) && new Date(s as string).getTime() < Date.now()
}

/** True when an expiry is within `hours` of now (and not already past). */
export function expiringSoon(s: string | undefined, hours = 24): boolean {
  if (!s) return false
  const ms = new Date(s).getTime() - Date.now()
  return ms > 0 && ms < hours * 3_600_000
}

export function relativeExpiry(s?: string | null): string {
  if (!s) return 'never'
  const ms = new Date(s).getTime() - Date.now()
  if (ms <= 0) return 'expired'
  const days = Math.floor(ms / 86_400_000)
  if (days >= 1) return `in ${days}d`
  const hours = Math.floor(ms / 3_600_000)
  return hours >= 1 ? `in ${hours}h` : 'in <1h'
}

/** "3 days ago" for timestamps in the past. */
export function relativePast(s?: string | null): string {
  if (!s) return 'never'
  const ms = Date.now() - new Date(s).getTime()
  if (ms < 60_000) return 'just now'
  const minutes = Math.floor(ms / 60_000)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(ms / 3_600_000)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(ms / 86_400_000)
  if (days < 30) return `${days}d ago`
  const months = Math.floor(days / 30)
  return months < 12 ? `${months}mo ago` : `${Math.floor(months / 12)}y ago`
}

/** Live countdown, for the device-approval expiry. */
export function countdown(s: string): string {
  const ms = new Date(s).getTime() - Date.now()
  if (ms <= 0) return 'expired'
  const total = Math.floor(ms / 1000)
  const m = Math.floor(total / 60)
  const sec = total % 60
  return `${m}:${String(sec).padStart(2, '0')}`
}

/**
 * How a value is displayed in an explorer cell. `null` must stay distinguishable
 * from the string "null" and from the empty string, because all three are
 * legitimate column values and confusing them corrupts a row on save.
 */
export function cellText(v: unknown): string {
  if (v === null || v === undefined) return 'NULL'
  if (typeof v === 'object') return JSON.stringify(v)
  return String(v)
}

/** The string form of a value as it goes into the row editor. Round-trips
 *  through the same text the cell shows, so an untouched field submits
 *  unchanged. NULL is represented by the checkbox, not by this string. */
export function inputValue(v: unknown): string {
  if (v === null || v === undefined) return ''
  if (typeof v === 'object') return JSON.stringify(v)
  return String(v)
}

/** Pretty-print for the expanded-cell popover. */
export function expandedText(v: unknown): string {
  if (v === null || v === undefined) return 'NULL'
  if (typeof v === 'object') return JSON.stringify(v, null, 2)
  const s = String(v)
  if (s.startsWith('{') || s.startsWith('[')) {
    try {
      return JSON.stringify(JSON.parse(s), null, 2)
    } catch {
      /* not JSON after all */
    }
  }
  return s
}

export function truncate(s: string, max = 48): string {
  return s.length > max ? s.slice(0, max - 1) + '…' : s
}

/** A .env block for the "copy as .env" affordance. */
export function envFile(connectionString: string): string {
  return `DATABASE_URL="${connectionString}"`
}

/** Columns that make up the primary key. A table without one is read-only. */
export function primaryKeyColumns(columns: { name: string; primary_key: boolean }[]): string[] {
  return columns.filter((c) => c.primary_key).map((c) => c.name)
}
