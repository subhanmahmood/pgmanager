import { useState } from 'react'
import { useNavigate } from 'react-router'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { toast } from 'sonner'
import { AlertTriangle, Loader2, LogOut, Trash2 } from 'lucide-react'
import { api } from '@/lib/api'
import { isAdmin, isHuman, principalLabel } from '@/lib/scopes'
import { useCleanup, useHealth, useWhoami } from '@/hooks/queries'
import { fmtDate } from '@/lib/format'
import type { CleanupResult } from '@/lib/types'
import { PageHeader } from '@/components/page-header'
import { ThemeToggleGroup } from '@/components/theme-toggle'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Alert, AlertDescription } from '@/components/ui/alert'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'

const passwordSchema = z
  .object({
    current: z.string().min(1, 'Required'),
    next: z.string().min(12, 'At least 12 characters'),
    confirm: z.string().min(1, 'Required'),
  })
  .refine((v) => v.next === v.confirm, {
    message: "Passwords don't match",
    path: ['confirm'],
  })

export function SettingsPage() {
  const { data: who } = useWhoami()
  const { data: health } = useHealth()
  const navigate = useNavigate()
  const qc = useQueryClient()

  const [olderThan, setOlderThan] = useState('7d')
  const [cleanupOpen, setCleanupOpen] = useState(false)
  const [cleanupResult, setCleanupResult] = useState<CleanupResult | null>(null)
  const cleanup = useCleanup()

  const signOut = useMutation({
    mutationFn: api.logout,
    onSettled: () => {
      qc.clear()
      navigate('/login', { replace: true })
    },
  })

  const form = useForm<z.infer<typeof passwordSchema>>({
    resolver: zodResolver(passwordSchema),
    defaultValues: { current: '', next: '', confirm: '' },
  })

  const changePassword = useMutation({
    mutationFn: (v: z.infer<typeof passwordSchema>) => api.changePassword(v.current, v.next),
    onSuccess: () => {
      // The server drops every session for this user, so there is nothing left
      // to stay signed in with.
      qc.clear()
      navigate('/login', {
        replace: true,
        state: { notice: 'Password changed. Sign in again.' },
      })
    },
    onError: (err) =>
      form.setError('root', { message: err instanceof Error ? err.message : 'Failed' }),
  })

  return (
    <div className="max-w-2xl space-y-6">
      <PageHeader title="Settings" />

      <Card>
        <CardHeader>
          <CardTitle className="text-sm">Session</CardTitle>
          <CardDescription>Who this browser is signed in as.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="space-y-1">
            <Label className="text-muted-foreground text-xs">Signed in as</Label>
            <p className="font-mono text-sm">{principalLabel(who)}</p>
          </div>
          <div className="space-y-1.5">
            <Label className="text-muted-foreground text-xs">Scopes</Label>
            <div className="flex flex-wrap gap-1.5">
              {(who?.scopes ?? []).map((s) => (
                <Badge key={s} variant="secondary" className="font-mono text-[11px]">
                  {s}
                </Badge>
              ))}
            </div>
          </div>
          <Button
            variant="outline"
            size="sm"
            onClick={() => signOut.mutate()}
            disabled={signOut.isPending}
          >
            <LogOut className="size-4" />
            Sign out
          </Button>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-sm">Appearance</CardTitle>
          <CardDescription>Follows your system by default.</CardDescription>
        </CardHeader>
        <CardContent>
          <ThemeToggleGroup />
        </CardContent>
      </Card>

      {/* A bearer principal has no password to change. */}
      {isHuman(who) && (
        <Card>
          <CardHeader>
            <CardTitle className="text-sm">Change password</CardTitle>
            <CardDescription>
              Changing it signs you out of every browser, including this one.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Form {...form}>
              <form
                onSubmit={form.handleSubmit((v) => changePassword.mutate(v))}
                className="space-y-4"
                noValidate
              >
                <FormField
                  control={form.control}
                  name="current"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Current password</FormLabel>
                      <FormControl>
                        <Input type="password" autoComplete="current-password" {...field} />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name="next"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>New password</FormLabel>
                      <FormControl>
                        <Input type="password" autoComplete="new-password" {...field} />
                      </FormControl>
                      <FormDescription>At least 12 characters.</FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name="confirm"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Confirm new password</FormLabel>
                      <FormControl>
                        <Input type="password" autoComplete="new-password" {...field} />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                {form.formState.errors.root && (
                  <Alert variant="destructive">
                    <AlertDescription>{form.formState.errors.root.message}</AlertDescription>
                  </Alert>
                )}
                <Button type="submit" disabled={changePassword.isPending}>
                  {changePassword.isPending && <Loader2 className="size-4 animate-spin" />}
                  Change password
                </Button>
              </form>
            </Form>
          </CardContent>
        </Card>
      )}

      {isAdmin(who) && (
        <Card className="border-destructive/30">
          <CardHeader>
            <CardTitle className="text-sm">Clean up expired PR databases</CardTitle>
            <CardDescription>
              Drops every PR database whose TTL has passed. This deletes data.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="flex flex-wrap items-end gap-3">
              <div className="space-y-1.5">
                <Label htmlFor="older-than" className="text-xs">
                  Older than
                </Label>
                <Input
                  id="older-than"
                  value={olderThan}
                  onChange={(e) => setOlderThan(e.target.value)}
                  className="w-28 font-mono"
                  placeholder="7d"
                />
              </div>
              <Button variant="destructive" onClick={() => setCleanupOpen(true)}>
                <Trash2 className="size-4" />
                Run cleanup
              </Button>
            </div>
            <p className="text-muted-foreground text-xs">
              Go duration or day form — <code className="font-mono">7d</code>,{' '}
              <code className="font-mono">168h</code>.
            </p>

            {cleanupResult && (
              <Alert>
                <AlertDescription>
                  {cleanupResult.count === 0 ? (
                    'Nothing to clean up.'
                  ) : (
                    <div className="space-y-1">
                      <p>
                        Deleted {cleanupResult.count} database
                        {cleanupResult.count === 1 ? '' : 's'}:
                      </p>
                      <ul className="font-mono text-xs">
                        {cleanupResult.deleted.map((d) => (
                          <li key={d}>{d}</li>
                        ))}
                      </ul>
                    </div>
                  )}
                </AlertDescription>
              </Alert>
            )}
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader>
          <CardTitle className="text-sm">Server</CardTitle>
        </CardHeader>
        <CardContent className="text-muted-foreground space-y-1 text-sm">
          <p>
            Status: <span className="text-foreground">{health?.status ?? 'unreachable'}</span>
          </p>
          <p>Server time: {fmtDate(health?.time)}</p>
        </CardContent>
      </Card>

      <ConfirmDialog
        open={cleanupOpen}
        onOpenChange={setCleanupOpen}
        title="Run cleanup?"
        description={
          <p>
            Every PR database older than{' '}
            <code className="bg-muted rounded px-1 font-mono text-xs">{olderThan}</code> will be
            dropped. This deletes data and cannot be undone.
          </p>
        }
        confirmLabel="Run cleanup"
        pending={cleanup.isPending}
        error={cleanup.error instanceof Error ? cleanup.error.message : null}
        onConfirm={() =>
          cleanup.mutate(olderThan, {
            onSuccess: (res) => {
              setCleanupResult(res)
              setCleanupOpen(false)
              toast.success(
                res.count === 0 ? 'Nothing to clean up' : `Deleted ${res.count} database(s)`,
              )
            },
          })
        }
      >
        <Alert variant="destructive">
          <AlertTriangle />
          <AlertDescription>This runs across every project.</AlertDescription>
        </Alert>
      </ConfirmDialog>
    </div>
  )
}
