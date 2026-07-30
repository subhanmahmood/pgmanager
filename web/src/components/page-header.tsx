import { cn } from '@/lib/utils'

export function PageHeader({
  title,
  description,
  actions,
  className,
  mono,
}: {
  title: React.ReactNode
  description?: React.ReactNode
  actions?: React.ReactNode
  className?: string
  mono?: boolean
}) {
  return (
    <div
      className={cn(
        'flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between',
        className,
      )}
    >
      <div className="min-w-0 space-y-1">
        <h1
          className={cn(
            'truncate text-xl font-semibold tracking-tight',
            mono && 'font-mono text-lg',
          )}
        >
          {title}
        </h1>
        {description && <p className="text-muted-foreground text-sm">{description}</p>}
      </div>
      {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
    </div>
  )
}
