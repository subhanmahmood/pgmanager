import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'
import { envSegment } from '@/lib/format'
import type { DatabaseInfo } from '@/lib/types'

const TONE: Record<string, string> = {
  dev: 'border-env-dev/35 text-env-dev bg-env-dev/10',
  staging: 'border-env-staging/35 text-env-staging bg-env-staging/10',
  prod: 'border-env-prod/40 text-env-prod bg-env-prod/10',
  pr: 'border-env-pr/35 text-env-pr bg-env-pr/10',
  scratch: 'border-env-scratch/35 text-env-scratch bg-env-scratch/10',
}

export function EnvBadge({
  db,
  className,
}: {
  db: Pick<DatabaseInfo, 'env' | 'key' | 'pr_number'>
  className?: string
}) {
  return (
    <Badge
      variant="outline"
      className={cn('font-mono text-[11px] tracking-tight', TONE[db.env], className)}
    >
      {envSegment(db)}
    </Badge>
  )
}

export function envTone(env: string): string {
  return TONE[env] ?? TONE.dev
}
