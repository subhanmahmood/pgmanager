import { AlertCircle, Lock, type LucideIcon } from 'lucide-react'
import { ApiError } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

export function EmptyState({
  icon: Icon,
  title,
  description,
  action,
  className,
}: {
  icon?: LucideIcon
  title: string
  description?: string
  action?: React.ReactNode
  className?: string
}) {
  return (
    <div
      className={cn(
        'flex flex-col items-center justify-center gap-3 px-6 py-14 text-center',
        className,
      )}
    >
      {Icon && (
        <div className="bg-muted text-muted-foreground rounded-full p-3">
          <Icon className="size-5" />
        </div>
      )}
      <div className="space-y-1">
        <p className="text-foreground text-sm font-medium">{title}</p>
        {description && (
          <p className="text-muted-foreground mx-auto max-w-sm text-sm">{description}</p>
        )}
      </div>
      {action}
    </div>
  )
}

/**
 * 403 is not an error to retry — it means this principal is not allowed to see
 * this, which is a legitimate steady state for a scoped bearer token.
 */
export function ErrorState({
  error,
  onRetry,
  className,
}: {
  error: unknown
  onRetry?: () => void
  className?: string
}) {
  const forbidden = error instanceof ApiError && error.status === 403
  const message = error instanceof Error ? error.message : String(error)

  return (
    <div
      className={cn(
        'flex flex-col items-center justify-center gap-3 px-6 py-14 text-center',
        className,
      )}
      role="alert"
    >
      <div
        className={cn(
          'rounded-full p-3',
          forbidden ? 'bg-muted text-muted-foreground' : 'bg-destructive/10 text-destructive',
        )}
      >
        {forbidden ? <Lock className="size-5" /> : <AlertCircle className="size-5" />}
      </div>
      <div className="space-y-1">
        <p className="text-sm font-medium">
          {forbidden ? 'Not permitted' : 'Something went wrong'}
        </p>
        <p className="text-muted-foreground mx-auto max-w-md text-sm">
          {forbidden ? "This token doesn't have permission to view this." : message}
        </p>
      </div>
      {!forbidden && onRetry && (
        <Button variant="outline" size="sm" onClick={onRetry}>
          Try again
        </Button>
      )}
    </div>
  )
}

/** Skeleton rows sized to the real table, so the layout does not jump. */
export function TableSkeleton({ columns, rows = 4 }: { columns: number; rows?: number }) {
  return (
    <div className="divide-border divide-y" aria-busy="true" aria-label="Loading">
      {Array.from({ length: rows }).map((_, r) => (
        <div key={r} className="flex items-center gap-4 px-4 py-3">
          {Array.from({ length: columns }).map((_, c) => (
            <Skeleton
              key={c}
              className="h-4"
              style={{ width: `${[28, 14, 18, 12, 16, 10][c % 6]}%` }}
            />
          ))}
        </div>
      ))}
    </div>
  )
}
