import { useHealth } from '@/hooks/queries'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { fmtDate } from '@/lib/format'
import { cn } from '@/lib/utils'

export function HealthDot({ withLabel = true }: { withLabel?: boolean }) {
  const { data, isError, isPending } = useHealth()
  const ok = !isError && !isPending && data?.status === 'ok'
  const state = isPending ? 'checking' : ok ? 'connected' : 'unreachable'

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          className="text-muted-foreground inline-flex cursor-default items-center gap-1.5 text-xs"
          role="status"
          aria-label={`Server ${state}`}
        >
          <span
            className={cn(
              'size-2 rounded-full transition-colors',
              isPending && 'bg-muted-foreground/40',
              !isPending && ok && 'bg-success',
              !isPending && !ok && 'bg-destructive animate-pulse',
            )}
          />
          {withLabel && <span className="hidden sm:inline">{state}</span>}
        </span>
      </TooltipTrigger>
      <TooltipContent>
        {ok ? `Server time ${fmtDate(data?.time)}` : 'Cannot reach the pgmanager server'}
      </TooltipContent>
    </Tooltip>
  )
}
