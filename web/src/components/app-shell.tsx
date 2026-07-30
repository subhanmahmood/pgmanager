import { useState } from 'react'
import { Link, NavLink, Outlet, useLocation, useNavigate } from 'react-router'
import { Database, KeyRound, LogOut, Menu, Settings, Smartphone, Terminal } from 'lucide-react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { canManageTokens, principalLabel } from '@/lib/scopes'
import { useDevices, useWhoami } from '@/hooks/queries'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from '@/components/ui/sheet'
import { HealthDot } from '@/components/health-dot'
import { ThemeToggle } from '@/components/theme-toggle'
import { CommandPalette } from '@/components/command-palette'
import { Breadcrumbs } from '@/components/breadcrumbs'
import { cn } from '@/lib/utils'

interface NavItem {
  to: string
  label: string
  icon: typeof Database
  requiresTokenScope?: boolean
}

const NAV: NavItem[] = [
  { to: '/projects', label: 'Projects', icon: Database },
  { to: '/tokens', label: 'Tokens', icon: KeyRound, requiresTokenScope: true },
  { to: '/devices', label: 'Devices', icon: Smartphone, requiresTokenScope: true },
  { to: '/settings', label: 'Settings', icon: Settings },
]

export function AppShell() {
  const { data: who } = useWhoami()
  const { data: devices } = useDevices(who)
  const [mobileOpen, setMobileOpen] = useState(false)
  const location = useLocation()
  const navigate = useNavigate()
  const qc = useQueryClient()

  const mayManageTokens = canManageTokens(who)
  const items = NAV.filter((i) => !i.requiresTokenScope || mayManageTokens)
  const pending = devices?.length ?? 0

  const signOut = useMutation({
    mutationFn: api.logout,
    onSettled: () => {
      qc.clear()
      navigate('/login', { replace: true })
    },
    onSuccess: () => toast.success('Signed out'),
  })

  return (
    <div className="flex min-h-dvh flex-col">
      <a
        href="#main"
        className="bg-primary text-primary-foreground focus:ring-ring sr-only rounded-md px-3 py-2 text-sm focus:not-sr-only focus:absolute focus:top-2 focus:left-2 focus:z-50"
      >
        Skip to content
      </a>

      <header className="bg-background/85 sticky top-0 z-40 border-b backdrop-blur-sm">
        <div className="mx-auto flex h-14 max-w-[1400px] items-center gap-2 px-4 sm:px-6">
          <Sheet open={mobileOpen} onOpenChange={setMobileOpen}>
            <SheetTrigger asChild>
              <Button variant="ghost" size="icon" className="md:hidden" aria-label="Open menu">
                <Menu className="size-4" />
              </Button>
            </SheetTrigger>
            <SheetContent side="left" className="w-64 p-0">
              <SheetHeader className="border-b">
                <SheetTitle className="font-mono text-sm">pgmanager</SheetTitle>
              </SheetHeader>
              <nav className="flex flex-col gap-1 p-3">
                {items.map(({ to, label, icon: Icon }) => (
                  <NavLink
                    key={to}
                    to={to}
                    onClick={() => setMobileOpen(false)}
                    className={({ isActive }) =>
                      cn(
                        'flex items-center gap-2.5 rounded-md px-3 py-2 text-sm transition-colors',
                        isActive
                          ? 'bg-accent text-accent-foreground font-medium'
                          : 'text-muted-foreground hover:bg-accent/60 hover:text-foreground',
                      )
                    }
                  >
                    <Icon className="size-4" />
                    {label}
                    {to === '/devices' && pending > 0 && (
                      <Badge variant="secondary" className="ml-auto">
                        {pending}
                      </Badge>
                    )}
                  </NavLink>
                ))}
              </nav>
            </SheetContent>
          </Sheet>

          <Link
            to="/projects"
            className="flex shrink-0 items-center gap-2 rounded font-mono text-sm font-medium"
          >
            <Terminal className="text-primary size-4" />
            <span className="hidden sm:inline">pgmanager</span>
          </Link>

          <nav className="ml-4 hidden items-center gap-1 md:flex" aria-label="Main">
            {items.map(({ to, label }) => {
              const active = location.pathname.startsWith(to)
              return (
                <NavLink
                  key={to}
                  to={to}
                  aria-current={active ? 'page' : undefined}
                  className={cn(
                    'relative rounded-md px-3 py-1.5 text-sm transition-colors',
                    active
                      ? 'text-foreground font-medium'
                      : 'text-muted-foreground hover:text-foreground',
                  )}
                >
                  <span className="inline-flex items-center gap-1.5">
                    {label}
                    {to === '/devices' && pending > 0 && (
                      <Badge
                        variant="secondary"
                        className="h-4 min-w-4 justify-center px-1 text-[10px] tabular-nums"
                      >
                        {pending}
                      </Badge>
                    )}
                  </span>
                  {active && (
                    <span className="bg-primary absolute inset-x-2 -bottom-[13px] h-0.5 rounded-full" />
                  )}
                </NavLink>
              )
            })}
          </nav>

          <div className="ml-auto flex items-center gap-1">
            <div className="mr-1 hidden lg:block">
              <HealthDot />
            </div>
            <CommandPalette />
            <ThemeToggle />
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button
                  variant="ghost"
                  size="sm"
                  className="max-w-[10rem] gap-1.5 font-mono text-xs"
                >
                  <span className="truncate">{principalLabel(who)}</span>
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-64">
                <DropdownMenuLabel className="space-y-1">
                  <p className="truncate font-mono text-xs font-normal">{principalLabel(who)}</p>
                  <p className="text-muted-foreground text-xs font-normal">
                    {who?.scopes?.length ?? 0} scope{who?.scopes?.length === 1 ? '' : 's'}
                    {who?.scopes?.includes('admin') && ' · admin'}
                  </p>
                </DropdownMenuLabel>
                <DropdownMenuSeparator />
                <DropdownMenuItem onSelect={() => navigate('/settings')}>
                  <Settings className="size-4" />
                  Settings
                </DropdownMenuItem>
                <DropdownMenuItem
                  variant="destructive"
                  onSelect={() => signOut.mutate()}
                  disabled={signOut.isPending}
                >
                  <LogOut className="size-4" />
                  Sign out
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </div>
        <Breadcrumbs />
      </header>

      <main id="main" className="mx-auto w-full max-w-[1400px] flex-1 px-4 py-6 sm:px-6 sm:py-8">
        <Outlet />
      </main>
    </div>
  )
}
