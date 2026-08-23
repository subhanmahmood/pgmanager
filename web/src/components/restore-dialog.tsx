import { useEffect, useState } from 'react'
import { Loader2 } from 'lucide-react'
import { toast } from 'sonner'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { SecretDialog } from '@/components/secret-dialog'
import { useRestoreBackup } from '@/hooks/queries'
import { envSegment, fmtDate } from '@/lib/format'
import type { Backup, DatabaseInfo, DatabaseSecret } from '@/lib/types'

/**
 * Restore always creates a brand-new database — the source is never opened,
 * let alone written to — so this is not a "type the name to confirm" gate
 * the way delete and rotate are. `confirm-dialog.tsx` reserves the typed
 * phrase for actions that destroy data they cannot get back; this one only
 * ever adds a database.
 */
export function RestoreDialog({
  db,
  backup,
  open,
  onOpenChange,
}: {
  db: DatabaseInfo
  backup: Backup
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const [result, setResult] = useState<DatabaseSecret | null>(null)
  const restore = useRestoreBackup(db.project, envSegment(db))

  useEffect(() => {
    if (open) restore.reset()
    // restore.reset is stable; re-running on every render would clear errors.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  return (
    <>
      <AlertDialog
        open={open && !result}
        onOpenChange={restore.isPending ? undefined : onOpenChange}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Restore backup #{backup.id}?</AlertDialogTitle>
            <AlertDialogDescription asChild>
              <div className="space-y-2 text-sm">
                <p>
                  Creates a brand-new database from the snapshot taken{' '}
                  <span className="text-foreground font-medium">{fmtDate(backup.started_at)}</span>,
                  with its own role and password.{' '}
                  <span className="text-foreground">
                    {db.database_name} is never opened and nothing about it changes.
                  </span>
                </p>
              </div>
            </AlertDialogDescription>
          </AlertDialogHeader>

          {restore.error && (
            <Alert variant="destructive">
              <AlertDescription>
                {restore.error instanceof Error ? restore.error.message : 'Restore failed'}
              </AlertDescription>
            </Alert>
          )}

          <AlertDialogFooter>
            <AlertDialogCancel disabled={restore.isPending}>Cancel</AlertDialogCancel>
            <AlertDialogAction
              disabled={restore.isPending}
              onClick={(e) => {
                e.preventDefault() // keep the dialog open so an error can render
                restore.mutate(backup.id, {
                  onSuccess: (secret) => {
                    setResult(secret)
                    toast.success('Backup restored')
                  },
                })
              }}
            >
              {restore.isPending && <Loader2 className="size-4 animate-spin" />}
              Restore backup
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {result && (
        <SecretDialog
          open
          tone="warning"
          onClose={() => {
            setResult(null)
            onOpenChange(false)
          }}
          title={`New database ${result.database_name}`}
          warning="This is a new database, created from the snapshot. Save its credentials now."
          note={`Reach it from now on as env "${result.env}".`}
          connectionString={result.connection_string}
          rows={[
            { label: 'Database', value: result.database_name },
            { label: 'Role', value: result.user_name },
            { label: 'Password', value: result.password, secret: true },
            { label: 'Host', value: `${result.host}:${result.port}` },
            { label: 'Connection string', value: result.connection_string, secret: true },
          ]}
        />
      )}
    </>
  )
}
