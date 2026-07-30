import { useState } from 'react'
import { Plus, X } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { useProjects } from '@/hooks/queries'
import { ENVIRONMENTS } from '@/lib/types'
import { scopeSeverity, validateScope } from '@/lib/scopes'
import { cn } from '@/lib/utils'

type Kind = 'admin' | 'tokens' | 'project:*' | 'project' | 'project:env' | 'project:pr'

const KINDS: { value: Kind; label: string; hint: string }[] = [
  { value: 'admin', label: 'admin', hint: 'Everything' },
  { value: 'tokens', label: 'tokens', hint: 'Token management only' },
  { value: 'project:*', label: 'project:*', hint: 'All projects, all environments' },
  { value: 'project', label: 'project:<name>', hint: 'One project, all environments' },
  { value: 'project:env', label: 'project:<name>:env:<env>', hint: 'One project, one environment' },
  { value: 'project:pr', label: 'project:<name>:pr:*', hint: 'One project, PR databases only' },
]

const TONE: Record<ReturnType<typeof scopeSeverity>, string> = {
  high: 'border-destructive/40 text-destructive bg-destructive/10',
  medium: 'border-warning/40 text-warning bg-warning/10',
  low: 'border-border text-muted-foreground',
}

/**
 * A bare textarea is where this UI was weakest, and a mistyped scope here is a
 * security bug rather than a cosmetic one. The composer builds only well-formed
 * strings; the raw editor stays available for anything it cannot express.
 */
export function ScopeComposer({
  scopes,
  onChange,
  /** Scopes present on the original request, for the device-approval diff. */
  requested,
  error,
}: {
  scopes: string[]
  onChange: (scopes: string[]) => void
  requested?: string[]
  error?: string | null
}) {
  const { data: projects } = useProjects()
  const [kind, setKind] = useState<Kind>('project')
  const [project, setProject] = useState('')
  const [env, setEnv] = useState<string>('dev')
  const [raw, setRaw] = useState(false)

  const needsProject = kind === 'project' || kind === 'project:env' || kind === 'project:pr'
  const assembled = (() => {
    switch (kind) {
      case 'admin':
        return 'admin'
      case 'tokens':
        return 'tokens'
      case 'project:*':
        return 'project:*'
      case 'project':
        return project ? `project:${project}` : ''
      case 'project:env':
        return project ? `project:${project}:env:${env}` : ''
      case 'project:pr':
        return project ? `project:${project}:pr:*` : ''
    }
  })()

  const add = () => {
    if (!assembled || validateScope(assembled)) return
    if (!scopes.includes(assembled)) onChange([...scopes, assembled])
  }

  const requestedSet = new Set(requested ?? [])
  const removed = (requested ?? []).filter((s) => !scopes.includes(s))

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <Label className="text-xs">Scopes</Label>
        <button
          type="button"
          onClick={() => setRaw((r) => !r)}
          className="text-muted-foreground hover:text-foreground rounded text-xs underline-offset-2 hover:underline"
        >
          {raw ? 'Use composer' : 'Edit as text'}
        </button>
      </div>

      {raw ? (
        <Textarea
          rows={4}
          className="font-mono text-xs"
          spellCheck={false}
          value={scopes.join('\n')}
          onChange={(e) =>
            onChange(
              e.target.value
                .split('\n')
                .map((s) => s.trim())
                .filter(Boolean),
            )
          }
          placeholder={'project:myapp:pr:*\nproject:other:env:dev'}
        />
      ) : (
        <div className="flex flex-wrap items-center gap-2">
          <Select
            value={kind}
            onValueChange={(v) => {
              setKind(v as Kind)
              if (!project && projects?.length) setProject(projects[0].name)
            }}
          >
            <SelectTrigger size="sm" className="w-[15rem] font-mono text-xs">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {KINDS.map((k) => (
                <SelectItem key={k.value} value={k.value}>
                  <span className="font-mono text-xs">{k.label}</span>
                  <span className="text-muted-foreground ml-2 text-xs">{k.hint}</span>
                </SelectItem>
              ))}
            </SelectContent>
          </Select>

          {needsProject && (
            <Select value={project} onValueChange={setProject}>
              <SelectTrigger size="sm" className="w-[10rem] font-mono text-xs">
                <SelectValue placeholder="project" />
              </SelectTrigger>
              <SelectContent>
                {(projects ?? []).map((p) => (
                  <SelectItem key={p.name} value={p.name} className="font-mono text-xs">
                    {p.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}

          {kind === 'project:env' && (
            <Select value={env} onValueChange={setEnv}>
              <SelectTrigger size="sm" className="w-[7rem] font-mono text-xs">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {ENVIRONMENTS.map((e) => (
                  <SelectItem key={e} value={e} className="font-mono text-xs">
                    {e}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}

          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={add}
            disabled={!assembled || scopes.includes(assembled)}
          >
            <Plus className="size-3.5" />
            Add
          </Button>

          {assembled && (
            <code className="text-muted-foreground w-full font-mono text-[11px]">
              {assembled}
            </code>
          )}
        </div>
      )}

      <div className="flex min-h-8 flex-wrap items-center gap-1.5">
        {scopes.length === 0 && (
          <span className="text-muted-foreground text-xs">No scopes selected.</span>
        )}
        {scopes.map((s) => {
          // On the approval screen, anything not asked for should be obvious.
          const added = requested !== undefined && !requestedSet.has(s)
          return (
            <Badge
              key={s}
              variant="outline"
              className={cn(
                'gap-1 font-mono text-[11px]',
                added ? 'border-warning/50 text-warning bg-warning/10' : TONE[scopeSeverity(s)],
              )}
            >
              {s}
              <button
                type="button"
                aria-label={`Remove scope ${s}`}
                onClick={() => onChange(scopes.filter((x) => x !== s))}
                className="hover:text-destructive rounded"
              >
                <X className="size-3" />
              </button>
            </Badge>
          )
        })}
        {removed.map((s) => (
          <Badge
            key={`removed-${s}`}
            variant="outline"
            className="text-muted-foreground/60 gap-1 font-mono text-[11px] line-through"
          >
            {s}
            <button
              type="button"
              aria-label={`Restore scope ${s}`}
              onClick={() => onChange([...scopes, s])}
              className="hover:text-foreground rounded no-underline"
            >
              <Plus className="size-3" />
            </button>
          </Badge>
        ))}
      </div>

      {error && <p className="text-destructive text-xs">{error}</p>}
    </div>
  )
}
