import { useCallback, useEffect, useRef, useState } from 'react'
import { Check, Copy, Eye, EyeOff } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { cn } from '@/lib/utils'

/** Falls back to selecting the text when the clipboard API is unavailable —
 *  which is the case on any non-HTTPS origin that isn't localhost. */
export async function copyText(value: string, input?: HTMLInputElement | null): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(value)
    return true
  } catch {
    if (input) {
      input.focus()
      input.select()
    }
    toast.error('Press ⌘C / Ctrl+C to copy')
    return false
  }
}

interface CopyFieldProps {
  label?: string
  value: string
  /** Masks the value behind a reveal toggle. Copy still works while hidden —
   *  the usual reason to touch a password is to paste it somewhere. */
  secret?: boolean
  className?: string
  /** Announced by screen readers on the copy button. */
  name?: string
}

export function CopyField({ label, value, secret, className, name }: CopyFieldProps) {
  const [copied, setCopied] = useState(false)
  const [revealed, setRevealed] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)

  useEffect(() => () => clearTimeout(timer.current), [])

  const onCopy = useCallback(async () => {
    const ok = await copyText(value, inputRef.current)
    if (!ok) return
    setCopied(true)
    clearTimeout(timer.current)
    timer.current = setTimeout(() => setCopied(false), 1500)
  }, [value])

  const hidden = secret && !revealed
  const what = name ?? label ?? 'value'

  return (
    <div className={cn('space-y-1.5', className)}>
      {label && <Label className="text-muted-foreground text-xs font-medium">{label}</Label>}
      <div className="flex items-center gap-1.5">
        <Input
          ref={inputRef}
          readOnly
          value={hidden ? '•'.repeat(Math.min(value.length, 32)) : value}
          onFocus={(e) => !hidden && e.currentTarget.select()}
          className="font-mono text-xs"
          aria-label={what}
        />
        {secret && (
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="shrink-0"
            onClick={() => setRevealed((v) => !v)}
            aria-label={revealed ? `Hide ${what}` : `Reveal ${what}`}
            title={revealed ? 'Hide' : 'Reveal'}
          >
            {revealed ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
          </Button>
        )}
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="shrink-0"
          onClick={onCopy}
          aria-label={`Copy ${what}`}
          title="Copy"
        >
          {copied ? <Check className="text-success size-4" /> : <Copy className="size-4" />}
        </Button>
      </div>
    </div>
  )
}

/** The inline variant used inside table cells and definition lists. */
export function CopyInline({
  value,
  className,
  label,
}: {
  value: string
  className?: string
  label?: string
}) {
  const [copied, setCopied] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  useEffect(() => () => clearTimeout(timer.current), [])

  return (
    <span className={cn('group/copy inline-flex min-w-0 items-center gap-1', className)}>
      <span className="truncate font-mono">{value}</span>
      <button
        type="button"
        aria-label={`Copy ${label ?? value}`}
        title="Copy"
        className="text-muted-foreground hover:text-foreground focus-visible:opacity-100 shrink-0 rounded opacity-0 transition-opacity group-hover/copy:opacity-100"
        onClick={async (e) => {
          e.stopPropagation()
          if (!(await copyText(value))) return
          setCopied(true)
          clearTimeout(timer.current)
          timer.current = setTimeout(() => setCopied(false), 1500)
        }}
      >
        {copied ? <Check className="text-success size-3.5" /> : <Copy className="size-3.5" />}
      </button>
    </span>
  )
}
