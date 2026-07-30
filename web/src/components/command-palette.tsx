import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router'
import { Database, KeyRound, Moon, Search, Settings, Smartphone, Sun } from 'lucide-react'
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from '@/components/ui/command'
import { Button } from '@/components/ui/button'
import { useProjects, useWhoami } from '@/hooks/queries'
import { canManageTokens } from '@/lib/scopes'
import { useTheme } from '@/lib/theme'

/** Cheap because everything it lists is already in the query cache. */
export function CommandPalette() {
  const [open, setOpen] = useState(false)
  const navigate = useNavigate()
  const { data: who } = useWhoami()
  const { data: projects } = useProjects()
  const { resolved, setTheme } = useTheme()
  const mayManageTokens = canManageTokens(who)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'k' && (e.metaKey || e.ctrlKey)) {
        e.preventDefault()
        setOpen((o) => !o)
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [])

  const go = (to: string) => {
    setOpen(false)
    navigate(to)
  }

  return (
    <>
      <Button
        variant="ghost"
        size="icon"
        onClick={() => setOpen(true)}
        aria-label="Search (Command K)"
        title="Search  ⌘K"
      >
        <Search className="size-4" />
      </Button>

      <CommandDialog
        open={open}
        onOpenChange={setOpen}
        title="Command palette"
        description="Jump to a project or section"
      >
        <CommandInput placeholder="Jump to a project or section…" />
        <CommandList>
          <CommandEmpty>Nothing found.</CommandEmpty>

          {projects && projects.length > 0 && (
            <CommandGroup heading="Projects">
              {projects.map((p) => (
                <CommandItem
                  key={p.name}
                  value={`project ${p.name}`}
                  onSelect={() => go(`/projects/${encodeURIComponent(p.name)}`)}
                >
                  <Database className="size-4" />
                  <span className="font-mono">{p.name}</span>
                </CommandItem>
              ))}
            </CommandGroup>
          )}

          <CommandSeparator />
          <CommandGroup heading="Go to">
            <CommandItem value="projects" onSelect={() => go('/projects')}>
              <Database className="size-4" />
              Projects
            </CommandItem>
            {mayManageTokens && (
              <>
                <CommandItem value="tokens api" onSelect={() => go('/tokens')}>
                  <KeyRound className="size-4" />
                  Tokens
                </CommandItem>
                <CommandItem value="devices authorization" onSelect={() => go('/devices')}>
                  <Smartphone className="size-4" />
                  Devices
                </CommandItem>
              </>
            )}
            <CommandItem value="settings preferences" onSelect={() => go('/settings')}>
              <Settings className="size-4" />
              Settings
            </CommandItem>
          </CommandGroup>

          <CommandSeparator />
          <CommandGroup heading="Actions">
            <CommandItem
              value="toggle theme dark light"
              onSelect={() => {
                setTheme(resolved === 'dark' ? 'light' : 'dark')
                setOpen(false)
              }}
            >
              {resolved === 'dark' ? <Sun className="size-4" /> : <Moon className="size-4" />}
              Switch to {resolved === 'dark' ? 'light' : 'dark'} theme
            </CommandItem>
          </CommandGroup>
        </CommandList>
      </CommandDialog>
    </>
  )
}
