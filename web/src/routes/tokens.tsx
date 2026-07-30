import { useMemo, useState } from 'react'
import { KeyRound, Loader2, Plus, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { useCreateToken, useRevokeToken, useTokens, useWhoami } from '@/hooks/queries'
import { canManageTokens, validateScopes } from '@/lib/scopes'
import { fmtDate, isExpired, relativeExpiry, relativePast } from '@/lib/format'
import type { CreatedToken, Token } from '@/lib/types'
import { PageHeader } from '@/components/page-header'
import { DataTable, type ColumnDef } from '@/components/data-table'
import { EmptyState, ErrorState } from '@/components/states'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { SecretDialog } from '@/components/secret-dialog'
import { ScopeComposer } from '@/components/scope-composer'
import { CopyInline } from '@/components/copy-field'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'

const EXPIRY_PRESETS = [
  { value: '30d', label: '30 days' },
  { value: '90d', label: '90 days' },
  { value: '365d', label: '1 year' },
  { value: 'never', label: 'Never' },
]

function status(t: Token): { label: string; className: string } {
  if (t.revoked_at)
    return { label: 'revoked', className: 'border-destructive/40 text-destructive' }
  if (isExpired(t.expires_at))
    return { label: 'expired', className: 'text-muted-foreground border-border' }
  return { label: 'active', className: 'border-success/40 text-success bg-success/10' }
}

export function TokensPage() {
  const { data: who } = useWhoami()
  const mayManage = canManageTokens(who)
  const { data: tokens, isPending, error, refetch } = useTokens(mayManage)

  const [showAll, setShowAll] = useState(false)
  const [createOpen, setCreateOpen] = useState(false)
  const [name, setName] = useState('')
  const [scopes, setScopes] = useState<string[]>([])
  const [expires, setExpires] = useState('90d')
  const [formError, setFormError] = useState<string | null>(null)
  const [created, setCreated] = useState<CreatedToken | null>(null)
  const [toRevoke, setToRevoke] = useState<Token | null>(null)

  const createToken = useCreateToken()
  const revokeToken = useRevokeToken()

  // The list keeps revoked rows forever, so it grows monotonically. Active is
  // what anyone actually wants to look at.
  const visible = useMemo(() => {
    if (!tokens) return tokens
    return showAll ? tokens : tokens.filter((t) => status(t).label === 'active')
  }, [tokens, showAll])

  const openCreate = () => {
    setName('')
    setScopes([])
    setExpires('90d')
    setFormError(null)
    createToken.reset()
    setCreateOpen(true)
  }

  const submit = () => {
    if (!name.trim()) return setFormError('A name is required')
    const scopeError = validateScopes(scopes)
    if (scopeError) return setFormError(scopeError)
    setFormError(null)

    createToken.mutate(
      { name: name.trim(), scopes, expires: expires === 'never' ? '' : expires },
      {
        onSuccess: (t) => {
          setCreateOpen(false)
          setCreated(t)
        },
        onError: (err) => setFormError(err instanceof Error ? err.message : 'Failed'),
      },
    )
  }

  const columns: ColumnDef<Token>[] = [
    {
      key: 'name',
      header: 'Name',
      cell: (t) => <span className="text-sm font-medium">{t.name}</span>,
    },
    {
      key: 'prefix',
      header: 'Prefix',
      cell: (t) => (
        <CopyInline value={t.token_prefix} className="text-muted-foreground text-xs" label="prefix" />
      ),
    },
    {
      key: 'scopes',
      header: 'Scopes',
      cell: (t) => (
        <div className="flex flex-wrap justify-end gap-1 md:justify-start">
          {t.scopes.slice(0, 2).map((s) => (
            <Badge key={s} variant="secondary" className="font-mono text-[11px]">
              {s}
            </Badge>
          ))}
          {t.scopes.length > 2 && (
            <Popover>
              <PopoverTrigger asChild>
                <button
                  type="button"
                  className="text-muted-foreground hover:text-foreground rounded text-xs"
                >
                  +{t.scopes.length - 2} more
                </button>
              </PopoverTrigger>
              <PopoverContent className="w-auto max-w-sm">
                <div className="flex flex-wrap gap-1">
                  {t.scopes.map((s) => (
                    <Badge key={s} variant="secondary" className="font-mono text-[11px]">
                      {s}
                    </Badge>
                  ))}
                </div>
              </PopoverContent>
            </Popover>
          )}
        </div>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      cell: (t) => {
        const s = status(t)
        return (
          <Badge variant="outline" className={`text-[11px] ${s.className}`}>
            {s.label}
          </Badge>
        )
      },
    },
    {
      key: 'last_used',
      header: 'Last used',
      cell: (t) =>
        t.last_used_at ? (
          <Tooltip>
            <TooltipTrigger asChild>
              <span className="text-muted-foreground text-xs">
                {relativePast(t.last_used_at)}
              </span>
            </TooltipTrigger>
            <TooltipContent>{fmtDate(t.last_used_at)}</TooltipContent>
          </Tooltip>
        ) : (
          <span className="text-muted-foreground/60 text-xs">never</span>
        ),
    },
    {
      key: 'expires',
      header: 'Expires',
      cell: (t) => (
        <Tooltip>
          <TooltipTrigger asChild>
            <span className="text-muted-foreground text-xs">
              {relativeExpiry(t.expires_at)}
            </span>
          </TooltipTrigger>
          <TooltipContent>{t.expires_at ? fmtDate(t.expires_at) : 'No expiry'}</TooltipContent>
        </Tooltip>
      ),
    },
    {
      key: 'actions',
      header: '',
      actions: true,
      cell: (t) =>
        t.revoked_at ? null : (
          <Button
            variant="ghost"
            size="icon"
            className="text-muted-foreground hover:text-destructive"
            onClick={() => setToRevoke(t)}
            aria-label={`Revoke ${t.name}`}
          >
            <Trash2 className="size-4" />
          </Button>
        ),
    },
  ]

  if (!mayManage) {
    return (
      <Card>
        <ErrorState error={{ status: 403 } as never} />
      </Card>
    )
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="API tokens"
        description="Machine credentials for CI and the CLI. A person mints one per client."
        actions={
          <Button onClick={openCreate}>
            <Plus className="size-4" />
            New token
          </Button>
        }
      />

      <div className="flex items-center justify-between">
        <ToggleGroup
          type="single"
          variant="outline"
          size="sm"
          value={showAll ? 'all' : 'active'}
          onValueChange={(v) => v && setShowAll(v === 'all')}
        >
          <ToggleGroupItem value="active" className="px-3 text-xs">
            Active
          </ToggleGroupItem>
          <ToggleGroupItem value="all" className="px-3 text-xs">
            All
          </ToggleGroupItem>
        </ToggleGroup>
        {tokens && (
          <span className="text-muted-foreground text-xs tabular">
            {visible?.length ?? 0} of {tokens.length}
          </span>
        )}
      </div>

      <Card className="overflow-hidden py-0">
        {error ? (
          <ErrorState error={error} onRetry={() => refetch()} />
        ) : (
          <DataTable
            columns={columns}
            rows={visible}
            rowKey={(t) => t.token_prefix}
            loading={isPending}
            empty={
              <EmptyState
                icon={KeyRound}
                title={showAll ? 'No tokens' : 'No active tokens'}
                description="Create a scoped token for a CI job or a CLI client."
                action={
                  <Button onClick={openCreate}>
                    <Plus className="size-4" />
                    New token
                  </Button>
                }
              />
            }
          />
        )}
      </Card>

      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent className="sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>New API token</DialogTitle>
            <DialogDescription>
              Scope it as narrowly as the client needs. A leaked token can do exactly what you
              grant here and nothing more.
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-4">
            <div className="space-y-1.5">
              <Label htmlFor="token-name" className="text-xs">
                Name
              </Label>
              <Input
                id="token-name"
                autoFocus
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="github-ci-myapp"
                className="font-mono"
                autoComplete="off"
              />
            </div>

            <ScopeComposer scopes={scopes} onChange={setScopes} />

            <div className="space-y-1.5">
              <Label className="text-xs">Expires</Label>
              <Select value={expires} onValueChange={setExpires}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {EXPIRY_PRESETS.map((p) => (
                    <SelectItem key={p.value} value={p.value}>
                      {p.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            {formError && (
              <Alert variant="destructive">
                <AlertDescription>{formError}</AlertDescription>
              </Alert>
            )}
          </div>

          <DialogFooter>
            <Button variant="outline" onClick={() => setCreateOpen(false)}>
              Cancel
            </Button>
            <Button onClick={submit} disabled={createToken.isPending}>
              {createToken.isPending && <Loader2 className="size-4 animate-spin" />}
              Create token
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {created && (
        <SecretDialog
          open
          onClose={() => setCreated(null)}
          title={created.info.name}
          warning="Copy this now — the plaintext token is never shown again."
          rows={[
            // Pre-revealed: a token is useless partially seen, and this is the
            // only moment it exists in the clear.
            { label: 'Token', value: created.token },
            { label: 'Prefix', value: created.token_prefix },
            { label: 'Scopes', value: created.info.scopes.join(' ') },
          ]}
        />
      )}

      <ConfirmDialog
        open={toRevoke !== null}
        onOpenChange={(o) => !o && setToRevoke(null)}
        title={`Revoke ${toRevoke?.name}?`}
        description={
          <p>
            Any client using{' '}
            <code className="bg-muted rounded px-1 font-mono text-xs">
              {toRevoke?.token_prefix}…
            </code>{' '}
            stops working immediately.
          </p>
        }
        confirmLabel="Revoke token"
        pending={revokeToken.isPending}
        error={revokeToken.error instanceof Error ? revokeToken.error.message : null}
        onConfirm={() =>
          toRevoke &&
          revokeToken.mutate(toRevoke.token_prefix, {
            onSuccess: () => {
              toast.success(`Revoked ${toRevoke.name}`)
              setToRevoke(null)
            },
          })
        }
      />
    </div>
  )
}
