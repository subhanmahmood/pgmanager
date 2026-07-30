import { Link, useLocation, useParams } from 'react-router'
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from '@/components/ui/breadcrumb'

interface Crumb {
  label: string
  to?: string
  mono?: boolean
}

/**
 * Only renders on nested routes, so flat pages keep a 56px header instead of a
 * 96px one. Derived from the path rather than from a context, so no screen has
 * to remember to declare its own trail.
 */
export function Breadcrumbs() {
  const { pathname } = useLocation()
  const params = useParams()
  const crumbs = buildCrumbs(pathname, params)
  if (crumbs.length < 2) return null

  return (
    <div className="bg-muted/25 border-t">
      <div className="mx-auto flex h-10 max-w-[1400px] items-center px-4 sm:px-6">
        <Breadcrumb>
          <BreadcrumbList className="text-xs">
            {crumbs.map((c, i) => {
              const last = i === crumbs.length - 1
              return (
                <BreadcrumbItem key={`${c.label}-${i}`}>
                  {last || !c.to ? (
                    <BreadcrumbPage className={c.mono ? 'font-mono' : undefined}>
                      {c.label}
                    </BreadcrumbPage>
                  ) : (
                    <>
                      <BreadcrumbLink asChild className={c.mono ? 'font-mono' : undefined}>
                        <Link to={c.to}>{c.label}</Link>
                      </BreadcrumbLink>
                      <BreadcrumbSeparator />
                    </>
                  )}
                </BreadcrumbItem>
              )
            })}
          </BreadcrumbList>
        </Breadcrumb>
      </div>
    </div>
  )
}

function buildCrumbs(pathname: string, params: Record<string, string | undefined>): Crumb[] {
  const { name, env } = params
  if (!pathname.startsWith('/projects') || !name) return []

  const crumbs: Crumb[] = [{ label: 'Projects', to: '/projects' }]
  crumbs.push({ label: name, to: `/projects/${encodeURIComponent(name)}`, mono: true })

  if (env) {
    const base = `/projects/${encodeURIComponent(name)}/databases/${encodeURIComponent(env)}`
    crumbs.push({ label: env, to: base, mono: true })
    if (pathname.endsWith('/explore')) {
      crumbs.push({ label: 'Explore', mono: false })
    }
  }
  return crumbs
}
