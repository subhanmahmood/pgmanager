import { useEffect, useMemo, useState } from 'react'
import { KeyRound, Loader2 } from 'lucide-react'
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
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { inputValue } from '@/lib/format'
import type { Column, Row } from '@/lib/types'
import { cn } from '@/lib/utils'

export type RowMode = 'insert' | 'edit'

interface FieldState {
  /** The text in the control. NULL is expressed by `isNull`, not by "". */
  text: string
  isNull: boolean
  /** Insert-only: let Postgres apply the column default. */
  useDefault: boolean
}

function initialState(columns: Column[], row: Row | null, mode: RowMode): Record<string, FieldState> {
  const out: Record<string, FieldState> = {}
  for (const col of columns) {
    const value = row ? row[col.name] : null
    out[col.name] = {
      text: inputValue(value),
      isNull: col.nullable && (value === null || value === undefined),
      // A column with a default — a serial primary key, a timestamp — should
      // normally be left to Postgres, so that is the starting choice.
      useDefault: mode === 'insert' && Boolean(col.default),
    }
  }
  return out
}

function isLongType(type: string): boolean {
  return /json|text|xml|bytea/i.test(type)
}

/**
 * Ported from the previous UI's row editor. Two behaviours are load-bearing and
 * easy to lose: NULL and "use default" are distinct from an empty string, and an
 * edit submits only the fields that actually changed, so an UPDATE never
 * rewrites a column it did not need to (and never fights a database-side
 * default or trigger).
 */
export function RowDialog({
  open,
  mode,
  columns,
  row,
  table,
  pending,
  error,
  onSubmit,
  onOpenChange,
}: {
  open: boolean
  mode: RowMode
  columns: Column[]
  row: Row | null
  table: string
  pending?: boolean
  error?: string | null
  onSubmit: (values: Row) => void
  onOpenChange: (open: boolean) => void
}) {
  const [fields, setFields] = useState<Record<string, FieldState>>({})

  useEffect(() => {
    if (open) setFields(initialState(columns, row, mode))
  }, [open, columns, row, mode])

  const set = (name: string, patch: Partial<FieldState>) =>
    setFields((f) => ({ ...f, [name]: { ...f[name], ...patch } }))

  const collect = useMemo(
    () => () => {
      const values: Row = {}
      for (const col of columns) {
        const f = fields[col.name]
        if (!f) continue
        if (f.useDefault) continue
        const value: string | null = f.isNull ? null : f.text

        if (mode === 'edit' && row) {
          const before = row[col.name]
          const beforeIsNull = before === null || before === undefined
          if (value === null && beforeIsNull) continue
          if (value !== null && !beforeIsNull && value === inputValue(before)) continue
        }
        values[col.name] = value
      }
      return values
    },
    [columns, fields, mode, row],
  )

  return (
    <Dialog open={open} onOpenChange={pending ? undefined : onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{mode === 'insert' ? 'Insert row' : 'Edit row'}</DialogTitle>
          <DialogDescription className="font-mono text-xs">{table}</DialogDescription>
        </DialogHeader>

        <form
          id="row-form"
          className="max-h-[55vh] space-y-4 overflow-y-auto pr-1"
          onSubmit={(e) => {
            e.preventDefault()
            onSubmit(collect())
          }}
        >
          {columns.map((col) => {
            const f = fields[col.name]
            if (!f) return null
            const long = isLongType(col.type)
            // The placeholder carries what will actually be sent, so the field
            // can stay editable instead of being disabled — typing is the
            // clearest way to say "no, I want a literal value here".
            const placeholder = f.useDefault
              ? col.default
                ? `default: ${col.default}`
                : 'default'
              : f.isNull
                ? 'NULL'
                : ''
            const overridden = f.isNull || f.useDefault
            const onType = (text: string) =>
              set(col.name, { text, isNull: false, useDefault: false })

            return (
              <div key={col.name} className="space-y-1.5">
                <div className="flex flex-wrap items-center gap-2">
                  <Label htmlFor={`f-${col.name}`} className="font-mono text-xs">
                    {col.name}
                  </Label>
                  <span className="text-muted-foreground font-mono text-[11px]">{col.type}</span>
                  {col.primary_key && (
                    <KeyRound className="text-warning size-3" aria-label="primary key" />
                  )}
                  <div className="ml-auto flex items-center gap-3">
                    {mode === 'insert' && col.default && (
                      <label className="text-muted-foreground flex items-center gap-1.5 text-xs">
                        <Checkbox
                          checked={f.useDefault}
                          onCheckedChange={(v) => set(col.name, { useDefault: v === true })}
                        />
                        default
                      </label>
                    )}
                    {col.nullable && (
                      <label className="text-muted-foreground flex items-center gap-1.5 text-xs">
                        <Checkbox
                          checked={f.isNull}
                          onCheckedChange={(v) => set(col.name, { isNull: v === true })}
                        />
                        NULL
                      </label>
                    )}
                  </div>
                </div>

                {long ? (
                  <Textarea
                    id={`f-${col.name}`}
                    value={overridden ? '' : f.text}
                    rows={3}
                    spellCheck={false}
                    placeholder={placeholder}
                    onChange={(e) => onType(e.target.value)}
                    className={cn(
                      'font-mono text-xs',
                      overridden && 'placeholder:text-muted-foreground/70 placeholder:italic',
                    )}
                  />
                ) : (
                  <Input
                    id={`f-${col.name}`}
                    value={overridden ? '' : f.text}
                    autoComplete="off"
                    spellCheck={false}
                    placeholder={placeholder}
                    onChange={(e) => onType(e.target.value)}
                    className={cn(
                      'font-mono text-xs',
                      overridden && 'placeholder:text-muted-foreground/70 placeholder:italic',
                    )}
                  />
                )}
              </div>
            )
          })}
        </form>

        {/* Constraint violations are the common failure here, so the dialog stays
            open with everything the operator typed still in place. */}
        {error && (
          <Alert variant="destructive">
            <AlertDescription className="font-mono text-xs">{error}</AlertDescription>
          </Alert>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={pending}>
            Cancel
          </Button>
          <Button type="submit" form="row-form" disabled={pending}>
            {pending && <Loader2 className="size-4 animate-spin" />}
            {mode === 'insert' ? 'Insert' : 'Save'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
