import { useEffect } from 'react'
import { Navigate, useLocation, useNavigate } from 'react-router'
import { Loader2 } from 'lucide-react'
import { ApiError } from '@/lib/api'
import { onSessionExpired } from '@/lib/query'
import { useWhoami } from '@/hooks/queries'

/**
 * Gate in front of the shell. Navigating (rather than assigning to
 * window.location) is what keeps the `?next=` round-trip intact — the device
 * deep link depends on surviving a sign-in.
 */
export function RequireAuth({ children }: { children: React.ReactNode }) {
  const { data, isPending, error } = useWhoami()
  const location = useLocation()
  const navigate = useNavigate()

  // A 401 raised by any later query, not just whoami.
  useEffect(
    () =>
      onSessionExpired(() => {
        navigate(`/login?next=${encodeURIComponent(location.pathname + location.search)}`, {
          replace: true,
          state: { notice: 'Your session expired. Sign in again.' },
        })
      }),
    [navigate, location.pathname, location.search],
  )

  if (isPending) {
    return (
      <div className="flex min-h-dvh items-center justify-center" aria-busy="true">
        <Loader2 className="text-muted-foreground size-5 animate-spin" />
        <span className="sr-only">Loading</span>
      </div>
    )
  }

  // 401 is "not signed in" — the ordinary first-visit case, not an error worth
  // showing. Anything else (a 403 on whoami, a network failure) still lands on
  // the login screen, because there is no session to work with either way.
  if (error || !data) {
    const unauthorized = error instanceof ApiError && error.status === 401
    return (
      <Navigate
        to={`/login?next=${encodeURIComponent(location.pathname + location.search)}`}
        replace
        state={unauthorized ? undefined : { notice: 'Sign in to continue.' }}
      />
    )
  }

  return <>{children}</>
}
