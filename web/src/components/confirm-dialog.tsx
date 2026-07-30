import { useEffect, useState } from 'react'
import { Loader2 } from 'lucide-react'
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
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { buttonVariants } from '@/components/ui/button'
import { cn } from '@/lib/utils'

interface ConfirmDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description: React.ReactNode
  confirmLabel?: string
  /** When set, the operator must type this exact string to enable the confirm
   *  button. Reserve it for actions that destroy data they cannot get back. */
  confirmPhrase?: string
  destructive?: boolean
  /** Rendered inside the dialog on failure, so the dialog stays open and the
   *  operator can retry without re-entering anything. */
  error?: string | null
  pending?: boolean
  onConfirm: () => void
  children?: React.ReactNode
}

export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel = 'Confirm',
  confirmPhrase,
  destructive = true,
  error,
  pending,
  onConfirm,
  children,
}: ConfirmDialogProps) {
  const [typed, setTyped] = useState('')

  useEffect(() => {
    if (open) setTyped('')
  }, [open])

  const gated = Boolean(confirmPhrase) && typed !== confirmPhrase
  const disabled = gated || pending

  return (
    <AlertDialog open={open} onOpenChange={pending ? undefined : onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription asChild>
            <div className="space-y-2 text-sm">{description}</div>
          </AlertDialogDescription>
        </AlertDialogHeader>

        {children}

        {confirmPhrase && (
          <div className="space-y-1.5">
            <Label htmlFor="confirm-phrase" className="text-muted-foreground text-xs">
              Type <span className="text-foreground font-mono">{confirmPhrase}</span> to confirm
            </Label>
            <Input
              id="confirm-phrase"
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
              autoComplete="off"
              spellCheck={false}
              className="font-mono"
            />
          </div>
        )}

        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}

        <AlertDialogFooter>
          {/* Cancel is the default focus target: the safe action should be the
              one you hit by reflex. */}
          <AlertDialogCancel disabled={pending}>Cancel</AlertDialogCancel>
          <AlertDialogAction
            disabled={disabled}
            onClick={(e) => {
              e.preventDefault() // keep the dialog open so errors can render
              onConfirm()
            }}
            className={cn(
              destructive &&
                buttonVariants({ variant: 'destructive' }) + ' hover:bg-destructive/90',
            )}
          >
            {pending && <Loader2 className="size-4 animate-spin" />}
            {confirmLabel}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
