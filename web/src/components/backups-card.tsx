import { useState } from 'react'
import { toast } from 'sonner'
import { AlertTriangle, DatabaseBackup, Loader2, RotateCcw, Trash2 } from 'lucide-react'
import {
  useBackups,
  useCreateBackup,
  useDeleteBackup,
  useSetBackupsEnabled,
} from '@/hooks/queries'
import { envSegment, fmtBytes, fmtDate } from '@/lib/format'
import { ApiError } from '@/lib/api'
import { toastError } from '@/lib/query'
import type { Backup, DatabaseInfo } from '@/lib/types'
import { Card, CardAction, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { Badge } from '@/components/ui/badge'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'
import { EmptyState } from '@/components/states'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { RestoreDialog } from '@/components/restore-dialog'

function StatusBadge({ status }: { status: Backup['status'] }) {
  if (status === 'succeeded') {
    return (
      <Badge variant="outline" className="border-success/40 text-success bg-success/10">
        succeeded
      </Badge>
    )
  }
  if (status === 'failed') {
    return <Badge variant="destructive">failed</Badge>
  }
  return (
    <Badge variant="outline" className="text-muted-foreground gap-1">
      <Loader2 className="size-3 animate-spin" />
      running
    </Badge>
  )
}

/**
 * Per-database snapshots to S3. Never rendered for a `pr` database — those
 * are throwaway by design and the server rejects the routes for them anyway,
 * but the card doesn't even offer the control there (database-detail.tsx).
 */
export function BackupsCard({ db }: { db: DatabaseInfo }) {
  const project = db.project
  const env = envSegment(db)

  const { data: backups, isPending, error } = useBackups(project, env)
  const setEnabled = useSetBackupsEnabled(project, env)
  const createBackup = useCreateBackup(project, env)
  const deleteBackup = useDeleteBackup(project, env)

  const [toDelete, setToDelete] = useState<Backup | null>(null)
  const [toRestore, setToRestore] = useState<Backup | null>(null)

  // Not configured is a steady state, not an error to retry — the server
  // returns the same 503 for every backup route, including this one.
  if (error instanceof ApiError && error.status === 503) {
    return (
      <Card>
        <CardHeader>
          <CardTitle className="text-sm">Backups</CardTitle>
        </CardHeader>
        <CardContent>
          <p className="text-muted-foreground text-sm">
            Backups are not configured on this server.
          </p>
        </CardContent>
      </Card>
    )
  }

  const newestFailure = backups?.find((b) => b.status === 'failed')

  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle className="text-sm">Backups</CardTitle>
          <CardAction>
            <Button
              variant="outline"
              size="sm"
              disabled={createBackup.isPending}
              onClick={() =>
                createBackup.mutate(undefined, {
                  onSuccess: () => toast.success('Backup started'),
                  onError: toastError,
                })
              }
            >
              {createBackup.isPending ? (
                <Loader2 className="size-4 animate-spin" />
              ) : (
                <DatabaseBackup className="size-4" />
              )}
              Back up now
            </Button>
          </CardAction>
        </CardHeader>
        <CardContent className="space-y-4">
          <label
            htmlFor="backups-enabled"
            className="hover:bg-accent/40 flex cursor-pointer items-center justify-between gap-3 rounded-lg border p-3 transition-colors"
          >
            <span className="space-y-1">
              <span className="block text-sm font-medium">Scheduled backups</span>
              <span className="text-muted-foreground block text-xs">
                Automatically back up this database on the server's schedule.
              </span>
            </span>
            <Switch
              id="backups-enabled"
              checked={db.backups_enabled ?? false}
              disabled={setEnabled.isPending}
              onCheckedChange={(checked) =>
                setEnabled.mutate(checked, {
                  onSuccess: () =>
                    toast.success(checked ? 'Scheduled backups enabled' : 'Scheduled backups disabled'),
                  onError: toastError,
                })
              }
            />
          </label>

          {newestFailure && (
            <Alert variant="destructive">
              <AlertTriangle />
              <AlertDescription>
                Backup #{newestFailure.id} failed: {newestFailure.error || 'unknown error'}
              </AlertDescription>
            </Alert>
          )}

          {isPending ? (
            <div className="space-y-2">
              <Skeleton className="h-8 w-full" />
              <Skeleton className="h-8 w-full" />
            </div>
          ) : !backups || backups.length === 0 ? (
            <EmptyState
              icon={DatabaseBackup}
              title="No backups yet"
              description="Run one now, or turn on scheduled backups above."
            />
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Status</TableHead>
                  <TableHead>Size</TableHead>
                  <TableHead>Started</TableHead>
                  <TableHead>Finished</TableHead>
                  <TableHead className="w-px" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {backups.map((b) => (
                  <TableRow key={b.id}>
                    <TableCell>
                      <StatusBadge status={b.status} />
                    </TableCell>
                    <TableCell className="tabular text-sm">{fmtBytes(b.size_bytes)}</TableCell>
                    <TableCell className="text-sm">{fmtDate(b.started_at)}</TableCell>
                    <TableCell className="text-sm">{fmtDate(b.finished_at)}</TableCell>
                    <TableCell className="text-right">
                      <div className="flex justify-end gap-1">
                        <Button
                          variant="ghost"
                          size="sm"
                          disabled={b.status !== 'succeeded'}
                          onClick={() => setToRestore(b)}
                        >
                          <RotateCcw className="size-4" />
                          Restore
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          className="text-destructive hover:bg-destructive/10 hover:text-destructive"
                          onClick={() => setToDelete(b)}
                        >
                          <Trash2 className="size-4" />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <ConfirmDialog
        open={toDelete !== null}
        onOpenChange={(o) => !o && setToDelete(null)}
        title={`Delete backup #${toDelete?.id}?`}
        description={<p>The stored snapshot is removed permanently. This cannot be undone.</p>}
        confirmLabel="Delete backup"
        pending={deleteBackup.isPending}
        error={deleteBackup.error instanceof Error ? deleteBackup.error.message : null}
        onConfirm={() =>
          toDelete &&
          deleteBackup.mutate(toDelete.id, {
            onSuccess: () => {
              toast.success('Backup deleted')
              setToDelete(null)
            },
          })
        }
      />

      {toRestore && (
        <RestoreDialog db={db} backup={toRestore} open onOpenChange={(o) => !o && setToRestore(null)} />
      )}
    </>
  )
}
