import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { TableSkeleton } from '@/components/states'
import { cn } from '@/lib/utils'

export interface ColumnDef<T> {
  /** Stable key, also used as the label on the mobile card layout. */
  key: string
  header: React.ReactNode
  /** Shown as the label below `md`. Defaults to `header` when it is a string. */
  mobileLabel?: string
  cell: (row: T) => React.ReactNode
  className?: string
  headerClassName?: string
  /** Actions column: no label on mobile, pinned right on desktop. */
  actions?: boolean
}

interface DataTableProps<T> {
  columns: ColumnDef<T>[]
  rows: T[] | undefined
  rowKey: (row: T) => string
  loading?: boolean
  onRowClick?: (row: T) => void
  empty?: React.ReactNode
  className?: string
}

/**
 * Deliberately thin. Nothing here needs client-side sorting or virtualisation —
 * the explorer paginates server-side and every other list is short — so pulling
 * in a table library would be all cost.
 *
 * Below `md` each row renders as a labelled card, because a six-column table on
 * a 375px screen is unusable and approving things from a phone is a real case.
 */
export function DataTable<T>({
  columns,
  rows,
  rowKey,
  loading,
  onRowClick,
  empty,
  className,
}: DataTableProps<T>) {
  if (loading) return <TableSkeleton columns={Math.min(columns.length, 6)} />
  if (!rows || rows.length === 0) return <>{empty}</>

  return (
    <div className={className}>
      {/* desktop */}
      <div className="hidden md:block">
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              {columns.map((c) => (
                <TableHead
                  key={c.key}
                  className={cn(
                    'text-muted-foreground text-xs font-medium',
                    c.actions && 'w-px text-right',
                    c.headerClassName,
                  )}
                >
                  {c.header}
                </TableHead>
              ))}
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((row) => (
              <TableRow
                key={rowKey(row)}
                onClick={onRowClick ? () => onRowClick(row) : undefined}
                className={cn(onRowClick && 'cursor-pointer')}
              >
                {columns.map((c) => (
                  <TableCell
                    key={c.key}
                    className={cn('py-2.5', c.actions && 'w-px text-right', c.className)}
                    onClick={c.actions ? (e) => e.stopPropagation() : undefined}
                  >
                    {c.cell(row)}
                  </TableCell>
                ))}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      {/* mobile */}
      <div className="space-y-2 md:hidden">
        {rows.map((row) => (
          <div
            key={rowKey(row)}
            className={cn('bg-card space-y-2 rounded-lg border p-3', onRowClick && 'cursor-pointer')}
            onClick={onRowClick ? () => onRowClick(row) : undefined}
          >
            {columns.map((c) => (
              <div
                key={c.key}
                className={cn('flex items-start justify-between gap-3', c.actions && 'pt-1')}
                onClick={c.actions ? (e) => e.stopPropagation() : undefined}
              >
                {!c.actions && (
                  <span className="text-muted-foreground shrink-0 text-xs">
                    {c.mobileLabel ?? (typeof c.header === 'string' ? c.header : c.key)}
                  </span>
                )}
                <div className={cn('min-w-0 text-sm', c.actions ? 'ml-auto' : 'text-right')}>
                  {c.cell(row)}
                </div>
              </div>
            ))}
          </div>
        ))}
      </div>
    </div>
  )
}
