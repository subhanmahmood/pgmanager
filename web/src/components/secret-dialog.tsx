import { AlertTriangle, FileDown } from 'lucide-react'
import { toast } from 'sonner'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { CopyField, copyText } from '@/components/copy-field'

export interface SecretRow {
  label: string
  value: string
  secret?: boolean
}

interface SecretDialogProps {
  open: boolean
  onClose: () => void
  title: string
  /** The consequence, stated plainly. */
  warning: string
  /** Softer second line — e.g. that a database password is re-fetchable, unlike
   *  a token. Honesty here beats manufactured urgency. */
  note?: string
  rows: SecretRow[]
  /** Enables the "Copy as .env" button. */
  connectionString?: string
  tone?: 'warning' | 'destructive'
}

export function SecretDialog({
  open,
  onClose,
  title,
  warning,
  note,
  rows,
  connectionString,
  tone = 'warning',
}: SecretDialogProps) {
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent
        className="sm:max-w-2xl"
        showCloseButton={false}
        // A one-shot secret must not be dismissible by a stray backdrop click or
        // an Escape reflex — the old UI could lose a freshly minted token that way.
        onInteractOutside={(e) => e.preventDefault()}
        onEscapeKeyDown={(e) => e.preventDefault()}
      >
        <DialogHeader>
          <DialogTitle className="font-mono text-base">{title}</DialogTitle>
          <DialogDescription className="sr-only">
            Credentials — copy them before closing this dialog.
          </DialogDescription>
        </DialogHeader>

        <Alert variant={tone === 'destructive' ? 'destructive' : 'default'}>
          <AlertTriangle />
          <AlertDescription>
            <span>{warning}</span>
            {note && <span className="text-muted-foreground block text-xs">{note}</span>}
          </AlertDescription>
        </Alert>

        <div className="space-y-3">
          {rows.map((r) => (
            <CopyField key={r.label} label={r.label} value={r.value} secret={r.secret} />
          ))}
        </div>

        <DialogFooter className="sm:justify-between">
          {connectionString ? (
            <Button
              type="button"
              variant="outline"
              onClick={async () => {
                if (await copyText(`DATABASE_URL="${connectionString}"`)) {
                  toast.success('Copied as .env')
                }
              }}
            >
              <FileDown className="size-4" />
              Copy as .env
            </Button>
          ) : (
            <span />
          )}
          <Button type="button" onClick={onClose}>
            Done
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
