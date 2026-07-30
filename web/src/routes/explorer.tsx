import { useEffect, useMemo, useRef, useState } from 'react'
import { Link, useParams, useSearchParams } from 'react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import {
  ChevronLeft,
  ChevronRight,
  KeyRound,
  MoreHorizontal,
  Plus,
  RotateCw,
  Search,
  Table2,
  Trash2,
} from 'lucide-react'
import { api, ApiError } from '@/lib/api'
import { keys } from '@/lib/query'
import { MAX_ROW_LIMIT, type Row, type TableRef } from '@/lib/types'
import { cellText, expandedText, primaryKeyColumns, truncate } from '@/lib/format'
import { useTables } from '@/hooks/queries'
import { EmptyState, ErrorState } from '@/components/states'
import { RowDialog, type RowMode } from '@/components/row-dialog'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { cn } from '@/lib/utils'

const PAGE_SIZES = [25, 50, 100, MAX_ROW_LIMIT]

export function ExplorerPage() {
  const { name = '', env = '' } = useParams()
  const [params, setParams] = useSearchParams()
  const qc = useQueryClient()

  // Everything that identifies "what am I looking at" lives in the URL, so
  // back/forward, reload and sharing a row page all work.
  const schema = params.get('schema') || 'public'
  const table = params.get('table') || ''
  const offset = Number(params.get('offset') || 0)
  const limit = Math.min(Number(params.get('limit') || 50), MAX_ROW_LIMIT)

  const [filter, setFilter] = useState('')
  const [rowMode, setRowMode] = useState<RowMode>('insert')
  const [editing, setEditing] = useState<Row | null>(null)
  const [rowOpen, setRowOpen] = useState(false)
  const [toDelete, setToDelete] = useState<Row | null>(null)
  const filterRef = useRef<HTMLInputElement>(null)

  const tables = useTables(name, env)

  const setParam = (patch: Record<string, string | null>) => {
    const next = new URLSearchParams(params)
    for (const [k, v] of Object.entries(patch)) {
      if (v === null) next.delete(k)
      else next.set(k, v)
    }
    setParams(next, { replace: false })
  }

  // "/" focuses the table filter, the way it does in every other data browser.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const el = document.activeElement
      const typing =
        el instanceof HTMLInputElement ||
        el instanceof HTMLTextAreaElement ||
        (el as HTMLElement | null)?.isContentEditable
      if (e.key === '/' && !typing) {
        e.preventDefault()
        filterRef.current?.focus()
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [])

  const page = useQuery({
    queryKey: keys.rows(name, env, schema, table, limit, offset),
    queryFn: () => api.listRows(name, env, table, { schema, limit, offset }),
    enabled: Boolean(table),
    // Keep the previous page on screen while the next one loads, but ONLY
    // within the same table. Carrying rows across a table switch is actively
    // dangerous: the mutations address `table` (already the new one) with a key
    // taken from a row belonging to the old one, so deleting the row you can
    // see destroys the same-id row in a different table. Paging is the case
    // this is for, and paging keeps the identity fixed.
    placeholderData: (prev, prevQuery) => {
      const key = prevQuery?.queryKey as ReturnType<typeof keys.rows> | undefined
      return key && key[3] === schema && key[4] === table ? prev : undefined
    },
  })

  const pk = useMemo(
    () => (page.data ? primaryKeyColumns(page.data.columns) : []),
    [page.data],
  )
  // A primary key is needed to address one existing row, so update and delete
  // require one. Insert does not — the server builds the INSERT from whatever
  // columns are supplied (internal/db/explore.go InsertRow), and ErrNoPrimaryKey
  // is raised only by update and delete.
  const canModifyRows = Boolean(page.data) && pk.length > 0
  const noPrimaryKey = Boolean(page.data) && pk.length === 0

  const rowKeyOf = (row: Row): Row => Object.fromEntries(pk.map((c) => [c, row[c]]))

  const invalidateRows = () =>
    qc.invalidateQueries({ queryKey: ['rows', name, env, schema, table] })

  const insertRow = useMutation({
    mutationFn: (values: Row) => api.insertRow(name, env, table, schema, { values }),
    onSuccess: () => {
      toast.success('Row inserted')
      setRowOpen(false)
      invalidateRows()
    },
  })

  const updateRow = useMutation({
    mutationFn: (values: Row) =>
      api.updateRow(name, env, table, schema, { key: rowKeyOf(editing as Row), values }),
    onSuccess: () => {
      toast.success('Row updated')
      setRowOpen(false)
      invalidateRows()
    },
  })

  const deleteRow = useMutation({
    mutationFn: (row: Row) => api.deleteRow(name, env, table, schema, { key: rowKeyOf(row) }),
    onSuccess: () => {
      toast.success('Row deleted')
      setToDelete(null)
      invalidateRows()
    },
  })

  const grouped = useMemo(() => groupTables(tables.data ?? [], filter), [tables.data, filter])
  const activeMutation = rowMode === 'insert' ? insertRow : updateRow

  const selectTable = (t: TableRef) =>
    setParam({ schema: t.schema, table: t.name, offset: '0' })

  return (
    <div className="-mt-2 flex h-[calc(100dvh-9.5rem)] flex-col gap-4 lg:flex-row">
      {/* table rail */}
      <aside className="bg-card flex w-full shrink-0 flex-col rounded-xl border lg:w-64">
        <div className="border-b p-2">
          <div className="relative">
            <Search className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2" />
            <Input
              ref={filterRef}
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder="Filter tables  /"
              className="h-8 pl-8 text-xs"
              aria-label="Filter tables"
            />
          </div>
        </div>

        <div className="min-h-0 flex-1 overflow-y-auto p-2">
          {tables.isPending ? (
            <div className="space-y-1.5 p-1">
              {Array.from({ length: 6 }).map((_, i) => (
                <Skeleton key={i} className="h-7 w-full" />
              ))}
            </div>
          ) : tables.error ? (
            <ErrorState error={tables.error} onRetry={() => tables.refetch()} className="py-6" />
          ) : grouped.length === 0 ? (
            <p className="text-muted-foreground p-3 text-center text-xs">No tables match.</p>
          ) : (
            grouped.map(([groupSchema, list]) => (
              <div key={groupSchema} className="mb-2">
                {/* The header is noise when public is the only schema. */}
                {grouped.length > 1 && (
                  <p className="text-muted-foreground px-2 py-1 font-mono text-[10px] tracking-wide uppercase">
                    {groupSchema}
                  </p>
                )}
                <ul>
                  {list.map((t) => {
                    const active = t.schema === schema && t.name === table
                    return (
                      <li key={`${t.schema}.${t.name}`}>
                        <button
                          type="button"
                          onClick={() => selectTable(t)}
                          className={cn(
                            'flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left font-mono text-xs transition-colors',
                            active
                              ? 'bg-accent text-accent-foreground font-medium'
                              : 'text-muted-foreground hover:bg-accent/50 hover:text-foreground',
                          )}
                          aria-current={active ? 'true' : undefined}
                        >
                          <Table2 className="size-3.5 shrink-0 opacity-60" />
                          <span className="truncate">{t.name}</span>
                        </button>
                      </li>
                    )
                  })}
                </ul>
              </div>
            ))
          )}
        </div>
      </aside>

      {/* rows */}
      <section className="bg-card flex min-h-0 min-w-0 flex-1 flex-col rounded-xl border">
        <div className="flex items-center gap-2 border-b px-3 py-2">
          <span className="truncate font-mono text-sm font-medium">
            {table ? `${schema}.${table}` : 'No table selected'}
          </span>
          {noPrimaryKey && (
            <Tooltip>
              <TooltipTrigger asChild>
                <Badge variant="outline" className="shrink-0 gap-1 text-[11px]">
                  <KeyRound className="size-3" />
                  no primary key
                </Badge>
              </TooltipTrigger>
              <TooltipContent className="max-w-xs">
                Without a primary key a row cannot be identified unambiguously, so the server
                refuses updates and deletes rather than risk matching more than one row. Inserting
                is still fine.
              </TooltipContent>
            </Tooltip>
          )}

          <div className="ml-auto flex items-center gap-1.5">
            <Button
              variant="ghost"
              size="icon"
              onClick={() => invalidateRows()}
              disabled={!table}
              aria-label="Refresh rows"
              title="Refresh"
            >
              <RotateCw className={cn('size-4', page.isFetching && 'animate-spin')} />
            </Button>
            {table && page.data && (
              <Button
                size="sm"
                onClick={() => {
                  setRowMode('insert')
                  setEditing(null)
                  insertRow.reset()
                  setRowOpen(true)
                }}
              >
                <Plus className="size-4" />
                Insert row
              </Button>
            )}
          </div>
        </div>

        <div className="min-h-0 flex-1 overflow-auto">
          {!table ? (
            <EmptyState
              icon={Table2}
              title="Pick a table"
              description="Choose a table on the left to browse its rows."
            />
          ) : page.isPending ? (
            <div className="space-y-2 p-4">
              {Array.from({ length: 8 }).map((_, i) => (
                <Skeleton key={i} className="h-6 w-full" />
              ))}
            </div>
          ) : page.error ? (
            // Permission denied, a dropped relation, a bad credential — all
            // stay inline. None of them mean the session is gone.
            <ErrorState error={page.error} onRetry={() => page.refetch()} />
          ) : page.data.rows.length === 0 ? (
            <EmptyState
              icon={Table2}
              title="No rows"
              description="This table is empty."
              action={
                <Button
                  variant="outline"
                  onClick={() => {
                    setRowMode('insert')
                    setEditing(null)
                    insertRow.reset()
                    setRowOpen(true)
                  }}
                >
                  <Plus className="size-4" />
                  Insert the first row
                </Button>

              }
            />
          ) : (
            <Table className="text-xs">
              <TableHeader className="bg-card sticky top-0 z-10">
                <TableRow className="hover:bg-transparent">
                  {page.data.columns.map((col) => (
                    <TableHead key={col.name} className="whitespace-nowrap">
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <span className="inline-flex items-center gap-1 font-mono">
                            {col.primary_key && <KeyRound className="text-warning size-3" />}
                            {col.name}
                          </span>
                        </TooltipTrigger>
                        <TooltipContent>
                          <span className="font-mono">
                            {col.type}
                            {col.nullable ? '' : ' NOT NULL'}
                          </span>
                        </TooltipContent>
                      </Tooltip>
                    </TableHead>
                  ))}
                  {canModifyRows && <TableHead className="w-px" />}
                </TableRow>
              </TableHeader>
              <TableBody>
                {page.data.rows.map((row, i) => (
                  <TableRow key={pk.length ? JSON.stringify(rowKeyOf(row)) : i}>
                    {page.data.columns.map((col) => (
                      <TableCell key={col.name} className="font-mono tabular whitespace-nowrap">
                        <Cell value={row[col.name]} />
                      </TableCell>
                    ))}
                    {canModifyRows && (
                      <TableCell className="w-px">
                        <DropdownMenu>
                          <DropdownMenuTrigger asChild>
                            <Button variant="ghost" size="icon" aria-label="Row actions">
                              <MoreHorizontal className="size-4" />
                            </Button>
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align="end">
                            <DropdownMenuItem
                              onSelect={() => {
                                setRowMode('edit')
                                setEditing(row)
                                updateRow.reset()
                                setRowOpen(true)
                              }}
                            >
                              Edit row
                            </DropdownMenuItem>
                            <DropdownMenuItem
                              variant="destructive"
                              onSelect={() => setToDelete(row)}
                            >
                              <Trash2 className="size-4" />
                              Delete row
                            </DropdownMenuItem>
                          </DropdownMenuContent>
                        </DropdownMenu>
                      </TableCell>
                    )}
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </div>

        {table && page.data && (
          <div className="flex flex-wrap items-center gap-3 border-t px-3 py-2">
            <span className="text-muted-foreground text-xs tabular">
              {page.data.total === 0
                ? '0 rows'
                : `${offset + 1}–${offset + page.data.rows.length} of ${page.data.total}`}
            </span>
            <Select
              value={String(limit)}
              onValueChange={(v) => setParam({ limit: v, offset: '0' })}
            >
              <SelectTrigger size="sm" className="h-7 w-[7.5rem] text-xs">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {PAGE_SIZES.map((n) => (
                  <SelectItem key={n} value={String(n)}>
                    {n} / page
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <div className="ml-auto flex gap-1.5">
              <Button
                variant="outline"
                size="sm"
                disabled={offset <= 0}
                onClick={() => setParam({ offset: String(Math.max(0, offset - limit)) })}
              >
                <ChevronLeft className="size-4" />
                Previous
              </Button>
              <Button
                variant="outline"
                size="sm"
                disabled={offset + page.data.rows.length >= page.data.total}
                onClick={() => setParam({ offset: String(offset + limit) })}
              >
                Next
                <ChevronRight className="size-4" />
              </Button>
            </div>
          </div>
        )}
      </section>

      {page.data && (
        <RowDialog
          open={rowOpen}
          mode={rowMode}
          columns={page.data.columns}
          row={editing}
          table={`${schema}.${table}`}
          pending={activeMutation.isPending}
          error={
            activeMutation.error instanceof ApiError ? activeMutation.error.message : null
          }
          onOpenChange={setRowOpen}
          onSubmit={(values) => {
            if (rowMode === 'insert') {
              insertRow.mutate(values)
              return
            }
            // Nothing was touched — closing is the honest response to a no-op.
            if (Object.keys(values).length === 0) {
              setRowOpen(false)
              return
            }
            updateRow.mutate(values)
          }}
        />
      )}

      <ConfirmDialog
        open={toDelete !== null}
        onOpenChange={(o) => !o && setToDelete(null)}
        title="Delete this row?"
        description={
          <p className="font-mono text-xs">
            {toDelete
              ? pk.map((c) => `${c}=${cellText(toDelete[c])}`).join(', ')
              : ''}
          </p>
        }
        confirmLabel="Delete row"
        pending={deleteRow.isPending}
        error={deleteRow.error instanceof Error ? deleteRow.error.message : null}
        onConfirm={() => toDelete && deleteRow.mutate(toDelete)}
      />

      <p className="sr-only">
        <Link to={`/projects/${encodeURIComponent(name)}/databases/${encodeURIComponent(env)}`}>
          Back to database
        </Link>
      </p>
    </div>
  )
}

/** NULL must stay visually distinct from the string "null" and from "". */
function Cell({ value }: { value: unknown }) {
  if (value === null || value === undefined) {
    return <span className="text-muted-foreground/60 italic">NULL</span>
  }
  const text = cellText(value)
  if (text.length <= 48) return <span>{text}</span>

  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          className="hover:text-primary text-left underline decoration-dotted underline-offset-2"
        >
          {truncate(text)}
        </button>
      </PopoverTrigger>
      <PopoverContent className="max-h-80 w-[32rem] overflow-auto">
        <pre className="font-mono text-xs whitespace-pre-wrap">{expandedText(value)}</pre>
      </PopoverContent>
    </Popover>
  )
}

/** public first — it is what people mean almost every time. */
function groupTables(tables: TableRef[], filter: string): [string, TableRef[]][] {
  const q = filter.trim().toLowerCase()
  const matched = q
    ? tables.filter(
        (t) => t.name.toLowerCase().includes(q) || t.schema.toLowerCase().includes(q),
      )
    : tables

  const bySchema = new Map<string, TableRef[]>()
  for (const t of matched) {
    const list = bySchema.get(t.schema) ?? []
    list.push(t)
    bySchema.set(t.schema, list)
  }
  return [...bySchema.entries()].sort(([a], [b]) => {
    if (a === 'public') return -1
    if (b === 'public') return 1
    return a.localeCompare(b)
  })
}
