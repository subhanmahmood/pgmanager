import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { toast } from 'sonner'
import { AlertTriangle, Loader2, Smartphone } from 'lucide-react'
import { api, ApiError } from '@/lib/api'
import { keys } from '@/lib/query'
import { countdown, fmtDate, relativeExpiry } from '@/lib/format'
import { canManageTokens, validateScopes } from '@/lib/scopes'
import { useApproveDevice, useDenyDevice, useDevices, useWhoami } from '@/hooks/queries'
import type { DeviceRequest } from '@/lib/types'
import { PageHeader } from '@/components/page-header'
import { DataTable, type ColumnDef } from '@/components/data-table'
import { EmptyState, ErrorState } from '@/components/states'
import { ScopeComposer } from '@/components/scope-composer'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Alert, AlertDescription } from '@/components/ui/alert'
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

const EXPIRY_PRESETS = [
  { value: '30d', label: '30 days' },
  { value: '90d', label: '90 days' },
  { value: '365d', label: '1 year' },
  { value: 'never', label: 'Never' },
]

/** Format as the server prints it: XXXX-XXXX, uppercase, hyphen auto-inserted. */
function formatCode(raw: string): string {
  const clean = raw.toUpperCase().replace(/[^A-Z0-9]/g, '').slice(0, 8)
  return clean.length > 4 ? `${clean.slice(0, 4)}-${clean.slice(4)}` : clean
}

export function DevicesPage() {
  const { data: who } = useWhoami()
  const mayManage = canManageTokens(who)
  const { data: devices, isPending, error, refetch, dataUpdatedAt } = useDevices(who)

  const [params, setParams] = useSearchParams()
  const [codeInput, setCodeInput] = useState('')
  const [reviewing, setReviewing] = useState<string | null>(null)

  // The CLI prints /device?code=XXXX-XXXX. Open straight into the review.
  useEffect(() => {
    const fromUrl = params.get('code')
    if (fromUrl) {
      setReviewing(formatCode(fromUrl))
      const next = new URLSearchParams(params)
      next.delete('code')
      setParams(next, { replace: true })
    }
  }, [params, setParams])

  const columns: ColumnDef<DeviceRequest>[] = [
    {
      key: 'code',
      header: 'Code',
      cell: (d) => <span className="font-mono text-sm font-medium">{d.user_code}</span>,
    },
    {
      key: 'client',
      header: 'Client',
      cell: (d) => <span className="text-sm">{d.client_name || '—'}</span>,
    },
    {
      key: 'ip',
      header: 'IP',
      cell: (d) => (
        <span className="text-muted-foreground font-mono text-xs">{d.client_ip || '—'}</span>
      ),
    },
    {
      key: 'scopes',
      header: 'Requested scopes',
      cell: (d) => (
        <div className="flex flex-wrap justify-end gap-1 md:justify-start">
          {(d.requested_scopes ?? []).length === 0 ? (
            <span className="text-muted-foreground/60 text-xs">none</span>
          ) : (
            d.requested_scopes!.map((s) => (
              <Badge key={s} variant="secondary" className="font-mono text-[11px]">
                {s}
              </Badge>
            ))
          )}
        </div>
      ),
    },
    {
      key: 'expires',
      header: 'Expires',
      cell: (d) => <ExpiryCell expiresAt={d.expires_at} />,
    },
    {
      key: 'actions',
      header: '',
      actions: true,
      cell: (d) => (
        <Button size="sm" variant="outline" onClick={() => setReviewing(d.user_code)}>
          Review
        </Button>
      ),
    },
  ]

  if (!mayManage) {
    return (
      <Card>
        <ErrorState error={new ApiError('forbidden', 403)} />
      </Card>
    )
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Device authorization"
        description="Approve a CLI or CI client that is waiting for a token."
        actions={
          <span className="text-muted-foreground text-xs">
            {dataUpdatedAt ? `updated ${new Date(dataUpdatedAt).toLocaleTimeString()}` : ''}
          </span>
        }
      />

      <Card>
        <CardHeader>
          <CardTitle className="text-sm">Enter a code</CardTitle>
        </CardHeader>
        <CardContent>
          <form
            className="flex flex-wrap items-end gap-3"
            onSubmit={(e) => {
              e.preventDefault()
              if (codeInput.length >= 8) setReviewing(codeInput)
            }}
          >
            <div className="space-y-1.5">
              <Label htmlFor="device-code" className="text-xs">
                Code shown by the client
              </Label>
              <Input
                id="device-code"
                value={codeInput}
                onChange={(e) => setCodeInput(formatCode(e.target.value))}
                placeholder="WXYZ-2468"
                className="w-44 font-mono text-base tracking-widest uppercase"
                autoComplete="off"
                spellCheck={false}
              />
            </div>
            <Button type="submit" disabled={codeInput.length < 9}>
              Review
            </Button>
          </form>
        </CardContent>
      </Card>

      <Card className="overflow-hidden py-0">
        {error ? (
          <ErrorState error={error} onRetry={() => refetch()} />
        ) : (
          <DataTable
            columns={columns}
            rows={devices}
            rowKey={(d) => d.user_code}
            loading={isPending}
            empty={
              <EmptyState
                icon={Smartphone}
                title="Nothing waiting"
                description="Run pgmanager login on a machine and its request appears here."
              />
            }
          />
        )}
      </Card>

      {reviewing && (
        <ApproveDialog
          code={reviewing}
          onClose={() => {
            setReviewing(null)
            setCodeInput('')
          }}
        />
      )}
    </div>
  )
}

/** Ticks once a second so an approver can see the window closing. */
function ExpiryCell({ expiresAt }: { expiresAt: string }) {
  const [, force] = useState(0)
  useEffect(() => {
    const id = setInterval(() => force((n) => n + 1), 1000)
    return () => clearInterval(id)
  }, [])

  const left = new Date(expiresAt).getTime() - Date.now()
  const urgent = left > 0 && left < 120_000

  return (
    <span
      className={`font-mono text-xs tabular ${
        left <= 0 ? 'text-destructive' : urgent ? 'text-warning' : 'text-muted-foreground'
      }`}
    >
      {left <= 0 ? 'expired' : countdown(expiresAt)}
    </span>
  )
}

function ApproveDialog({ code, onClose }: { code: string; onClose: () => void }) {
  const [scopes, setScopes] = useState<string[]>([])
  const [name, setName] = useState('')
  const [expires, setExpires] = useState('90d')
  const [formError, setFormError] = useState<string | null>(null)
  const [denyOpen, setDenyOpen] = useState(false)
  const [prefilled, setPrefilled] = useState(false)

  const approve = useApproveDevice()
  const deny = useDenyDevice()

  const request = useQuery({
    queryKey: keys.device(code),
    queryFn: () => api.getDevice(code),
    retry: false,
  })

  useEffect(() => {
    if (request.data && !prefilled) {
      setScopes(request.data.requested_scopes ?? [])
      setName(request.data.client_name || `device-${request.data.user_code}`)
      setPrefilled(true)
    }
  }, [request.data, prefilled])

  const expired = request.data ? new Date(request.data.expires_at).getTime() <= Date.now() : false

  const submit = () => {
    if (!name.trim()) return setFormError('A name is required')
    const scopeError = validateScopes(scopes)
    if (scopeError) return setFormError(scopeError)
    setFormError(null)

    approve.mutate(
      { code, body: { name: name.trim(), scopes, expires: expires === 'never' ? '' : expires } },
      {
        onSuccess: () => {
          // The plaintext goes to the waiting device by polling — there is
          // nothing to reveal here.
          toast.success(`Approved ${code}`)
          onClose()
        },
        onError: (err) => {
          if (err instanceof ApiError && err.status === 409) {
            toast.info('That request was already handled')
            onClose()
            return
          }
          setFormError(err instanceof Error ? err.message : 'Approval failed')
        },
      },
    )
  }

  return (
    <>
      <Dialog open onOpenChange={(o) => !o && onClose()}>
        <DialogContent className="sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>
              Authorize <span className="font-mono">{code}</span>
            </DialogTitle>
            <DialogDescription>
              Approving mints a token and hands it to the waiting client.
            </DialogDescription>
          </DialogHeader>

          {request.isPending ? (
            <div className="flex items-center justify-center py-10">
              <Loader2 className="text-muted-foreground size-5 animate-spin" />
            </div>
          ) : request.error ? (
            <ErrorState error={request.error} />
          ) : request.data ? (
            <div className="space-y-4">
              <dl className="bg-muted/40 grid grid-cols-2 gap-x-4 gap-y-2 rounded-lg border p-3 text-sm">
                <Row label="Client" value={request.data.client_name || '—'} />
                <Row label="IP" value={request.data.client_ip || '—'} mono />
                <Row label="Code" value={request.data.user_code} mono />
                <div className="space-y-0.5">
                  <dt className="text-muted-foreground text-xs">Expires</dt>
                  <dd>
                    <ExpiryCell expiresAt={request.data.expires_at} />
                  </dd>
                </div>
              </dl>

              <Alert>
                <AlertTriangle />
                <AlertDescription>
                  This device gets exactly the scopes you choose. What it asked for is only a
                  suggestion.
                </AlertDescription>
              </Alert>

              <div className="space-y-1.5">
                <Label htmlFor="approve-name" className="text-xs">
                  Token name
                </Label>
                <Input
                  id="approve-name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  className="font-mono"
                  autoComplete="off"
                />
              </div>

              {/* Prefilled from the request, but anything you add or remove is
                  called out so the approver sees what they changed. */}
              <ScopeComposer
                scopes={scopes}
                onChange={setScopes}
                requested={request.data.requested_scopes ?? []}
              />

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

              {expired && (
                <Alert variant="destructive">
                  <AlertDescription>
                    This request expired {relativeExpiry(request.data.expires_at)} — ask the client
                    to start again.
                  </AlertDescription>
                </Alert>
              )}

              {formError && (
                <Alert variant="destructive">
                  <AlertDescription>{formError}</AlertDescription>
                </Alert>
              )}
            </div>
          ) : null}

          <DialogFooter className="sm:justify-between">
            <Button variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <div className="flex gap-2">
              <Button
                variant="outline"
                className="text-destructive hover:bg-destructive/10"
                onClick={() => setDenyOpen(true)}
                disabled={!request.data}
              >
                Deny
              </Button>
              <Button onClick={submit} disabled={!request.data || expired || approve.isPending}>
                {approve.isPending && <Loader2 className="size-4 animate-spin" />}
                Approve
              </Button>
            </div>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <ConfirmDialog
        open={denyOpen}
        onOpenChange={setDenyOpen}
        title={`Deny ${code}?`}
        description={<p>The waiting client is told the request was refused. No token is issued.</p>}
        confirmLabel="Deny request"
        pending={deny.isPending}
        error={deny.error instanceof Error ? deny.error.message : null}
        onConfirm={() =>
          deny.mutate(code, {
            onSuccess: () => {
              toast.success(`Denied ${code}`)
              setDenyOpen(false)
              onClose()
            },
          })
        }
      />
    </>
  )
}

function Row({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="min-w-0 space-y-0.5">
      <dt className="text-muted-foreground text-xs">{label}</dt>
      <dd className={`truncate text-sm ${mono ? 'font-mono' : ''}`}>{value}</dd>
    </div>
  )
}

export { fmtDate }
