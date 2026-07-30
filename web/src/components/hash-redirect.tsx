import { useEffect, useRef } from 'react'
import { useNavigate } from 'react-router'

const TABS = new Set(['projects', 'tokens', 'devices', 'maintenance'])

/**
 * The previous UI routed on the hash, and those URLs are documented in the
 * README, so someone has them bookmarked. Runs once on first render and then
 * gets out of the way. Safe to delete a release or two after this ships.
 */
export function HashRedirect() {
  const navigate = useNavigate()
  const done = useRef(false)

  useEffect(() => {
    if (done.current) return
    done.current = true

    const hash = window.location.hash.replace(/^#/, '')
    if (!hash) return

    const [head, ...rest] = hash.split('/')
    let target: string | null = null

    if (head === 'explore' && rest.length >= 2) {
      const [project, env] = rest
      target = `/projects/${encodeURIComponent(project)}/databases/${encodeURIComponent(env)}/explore`
    } else if (TABS.has(head)) {
      // "maintenance" became "settings" — it held session and password, which
      // were never maintenance.
      target = head === 'maintenance' ? '/settings' : `/${head}`
    }

    if (target) {
      window.history.replaceState(null, '', window.location.pathname + window.location.search)
      navigate(target, { replace: true })
    }
  }, [navigate])

  return null
}
