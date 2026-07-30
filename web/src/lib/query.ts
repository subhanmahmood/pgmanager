import { MutationCache, QueryCache, QueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { ApiError } from './api'

/**
 * Signals that the session is gone. The router subscribes and navigates, which
 * keeps the `?next=` round-trip intact — a `window.location` assignment here
 * would throw away the URL the operator was trying to reach.
 */
type Listener = () => void
const expiredListeners = new Set<Listener>()

export function onSessionExpired(fn: Listener): () => void {
  expiredListeners.add(fn)
  return () => expiredListeners.delete(fn)
}

let notifiedExpired = false

function handleError(err: unknown) {
  if (!(err instanceof ApiError)) return

  // 401 means the session is genuinely gone. 403 does NOT: a scoped bearer
  // token legitimately gets 403 from /auth/tokens, and the old UI signed such
  // a principal straight out. 403 is handled per-view instead.
  if (err.status === 401) {
    if (notifiedExpired) return
    notifiedExpired = true
    queryClient.clear()
    expiredListeners.forEach((fn) => fn())
  }
}

/**
 * Two 401s are not session expiry and must not be announced as one:
 *
 *  - `whoami` on a cold load. That 401 IS the "not signed in yet" answer, and
 *    RequireAuth already routes on it. Treating it as expiry greets every
 *    first-time visitor with "your session expired".
 *  - a failed sign-in. The login endpoint answers bad credentials with 401,
 *    and the form renders the server's own message.
 */
function isAuthProbe(queryKey: readonly unknown[]): boolean {
  return queryKey[0] === 'whoami'
}

/** Mutations opt out with `meta: { skipAuthRedirect: true }`. */
function skipsAuthRedirect(meta: Record<string, unknown> | undefined): boolean {
  return meta?.skipAuthRedirect === true
}

/** Called after a successful sign-in so the next 401 notifies again. */
export function resetExpiredNotice() {
  notifiedExpired = false
}

export const queryClient = new QueryClient({
  queryCache: new QueryCache({
    onError: (err, query) => {
      if (isAuthProbe(query.queryKey)) return
      handleError(err)
    },
  }),
  mutationCache: new MutationCache({
    onError: (err, _vars, _ctx, mutation) => {
      if (skipsAuthRedirect(mutation.meta)) return
      handleError(err)
    },
  }),
  defaultOptions: {
    queries: {
      retry: false,
      staleTime: 30_000,
      refetchOnWindowFocus: false,
    },
  },
})

/** Toast for a mutation failure that has no better home than the corner.
 *  Anything with a form or a dialog should render the message inline instead. */
export function toastError(err: unknown) {
  if (err instanceof ApiError && err.status === 401) return // already redirecting
  toast.error(err instanceof Error ? err.message : String(err))
}

export const keys = {
  health: ['health'] as const,
  whoami: ['whoami'] as const,
  projects: ['projects'] as const,
  databases: (project: string) => ['databases', project] as const,
  database: (project: string, env: string) => ['database', project, env] as const,
  credentials: (project: string, env: string) => ['credentials', project, env] as const,
  tables: (project: string, env: string) => ['tables', project, env] as const,
  rows: (project: string, env: string, schema: string, table: string, limit: number, offset: number) =>
    ['rows', project, env, schema, table, limit, offset] as const,
  tokens: ['tokens'] as const,
  devices: ['devices'] as const,
  device: (code: string) => ['device', code] as const,
}
