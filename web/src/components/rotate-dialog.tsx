import { useEffect, useState } from 'react'
import { AlertTriangle, Loader2 } from 'lucide-react'
import { toast } from 'sonner'
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { SecretDialog } from '@/components/secret-dialog'
import { useRotatePassword } from '@/hooks/queries'
import type { DatabaseInfo, DatabaseSecret } from '@/lib/types'
import { envSegment } from '@/lib/format'

/**
 * Rotation is the most dangerous button in the app: it invalidates the password
 * every running application is holding. Three beats — decide, confirm, reveal —
 * with the friction scaled to the blast radius rather than applied uniformly,
 * because a gate you always hit is a gate you learn to type through.
 */
export function RotateDialog({
  db,
  open,
  onOpenChange,
}: {
  db: DatabaseInfo
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const [terminate, setTerminate] = useState(false)
  const [typed, setTyped] = useState('')
  const [result, setResult] = useState<DatabaseSecret | null>(null)

  const rotate = useRotatePassword(db.project, envSegment(db))

  useEffect(() => {
    if (open) {
      setTerminate(false)
      setTyped('')
      rotate.reset()
    }
    // rotate.reset is stable; re-running on every render would clear errors.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  // Production, or killing live connections, is where a mistake actually hurts.
  const needsPhrase = db.env === 'prod' || terminate
  const gated = needsPhrase && typed !== db.database_name

  return (
    <>
      <AlertDialog
        open={open && !result}
        onOpenChange={rotate.isPending ? undefined : onOpenChange}
      >
        <AlertDialogContent className="sm:max-w-lg">
          <AlertDialogHeader>
            <AlertDialogTitle>
              Rotate password for{' '}
              <span className="font-mono text-base">{db.database_name}</span>?
            </AlertDialogTitle>
            <AlertDialogDescription asChild>
              <div className="space-y-2 text-sm">
                <p>
                  A new password is generated immediately.{' '}
                  <strong className="text-foreground">
                    Every application using the current password will fail to authenticate on its
                    next connection.
                  </strong>
                </p>
                <p>
                  Connections that are already open keep working until they reconnect — unless you
                  terminate them.
                </p>
              </div>
            </AlertDialogDescription>
          </AlertDialogHeader>

          <label
            className="hover:bg-accent/40 flex cursor-pointer items-start gap-3 rounded-lg border p-3 transition-colors"
            htmlFor="terminate"
          >
            <Checkbox
              id="terminate"
              checked={terminate}
              onCheckedChange={(v) => setTerminate(v === true)}
              className="mt-0.5"
            />
            <span className="space-y-1">
              <span className="block text-sm font-medium">Terminate existing connections</span>
              <span className="text-muted-foreground block text-xs">
                Forces every current session to disconnect now. Use this if you are rotating
                because the old password may be compromised.
              </span>
            </span>
          </label>

          {needsPhrase && (
            <div className="space-y-1.5">
              <Label htmlFor="rotate-phrase" className="text-muted-foreground text-xs">
                Type <span className="text-foreground font-mono">{db.database_name}</span> to
                confirm
              </Label>
              <Input
                id="rotate-phrase"
                value={typed}
                onChange={(e) => setTyped(e.target.value)}
                autoComplete="off"
                spellCheck={false}
                className="font-mono"
              />
            </div>
          )}

          {/* The server returns the manager's own message; show it verbatim and
              keep the checkbox state so a retry is one click. */}
          {rotate.error && (
            <Alert variant="destructive">
              <AlertDescription>
                {rotate.error instanceof Error ? rotate.error.message : 'Rotation failed'}
              </AlertDescription>
            </Alert>
          )}

          <AlertDialogFooter>
            <AlertDialogCancel disabled={rotate.isPending}>Cancel</AlertDialogCancel>
            <Button
              variant={terminate ? 'destructive' : 'default'}
              disabled={gated || rotate.isPending}
              onClick={() =>
                rotate.mutate(terminate, {
                  onSuccess: (secret) => {
                    setResult(secret)
                    toast.success('Password rotated')
                  },
                })
              }
            >
              {rotate.isPending && <Loader2 className="size-4 animate-spin" />}
              {terminate ? 'Rotate and disconnect' : 'Rotate password'}
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {result && (
        <SecretDialog
          open
          tone="destructive"
          onClose={() => {
            setResult(null)
            onOpenChange(false)
          }}
          title={`New credentials for ${result.database_name}`}
          warning="The previous password no longer works. Update anything that connects with it."
          // Unlike a token, this is re-fetchable. Saying so removes the panic
          // without removing the urgency.
          note="You can retrieve it again from this database's Credentials."
          connectionString={result.connection_string}
          rows={[
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

/** The entry affordance. Outline, not destructive: rotation is maintenance, and
 *  painting maintenance red teaches people to ignore red. */
export function RotateTrigger({
  db,
  onClick,
}: {
  db: DatabaseInfo
  onClick: () => void
}) {
  return (
    <div className="space-y-1">
      <Button variant="outline" className="w-full justify-start" onClick={onClick}>
        {db.env === 'prod' ? (
          <AlertTriangle className="text-warning size-4" />
        ) : (
          <span className="size-4" />
        )}
        Rotate password
      </Button>
      <p className="text-muted-foreground px-1 text-xs">
        Issues a new password. Existing clients must be updated.
      </p>
    </div>
  )
}
