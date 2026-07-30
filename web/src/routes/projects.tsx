import { useState } from 'react'
import { Link } from 'react-router'
import { useQueries } from '@tanstack/react-query'
import { Database, FolderPlus, Loader2, MoreHorizontal, Plus, Trash2 } from 'lucide-react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { keys } from '@/lib/query'
import { useCreateProject, useDeleteProject, useProjects } from '@/hooks/queries'
import { fmtDate, relativePast } from '@/lib/format'
import { PageHeader } from '@/components/page-header'
import { EmptyState, ErrorState } from '@/components/states'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { Input } from '@/components/ui/input'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'

// Mirrors internal/project.ValidateName.
const RESERVED = ['postgres', 'template0', 'template1', 'admin', 'root', 'system']
const schema = z.object({
  name: z
    .string()
    .min(2, 'At least 2 characters')
    .max(32, 'At most 32 characters')
    .regex(/^[a-z][a-z0-9_]*$/, 'Lowercase letters, digits and underscores; must start with a letter')
    .refine((n) => !RESERVED.includes(n), 'That name is reserved'),
})

export function ProjectsPage() {
  const { data: projects, isPending, error, refetch } = useProjects()
  const [createOpen, setCreateOpen] = useState(false)
  const [toDelete, setToDelete] = useState<string | null>(null)

  const createProject = useCreateProject()
  const deleteProject = useDeleteProject()

  // One list request per project. There is no fleet-wide database endpoint, and
  // adding one is a server change; at this scale the fan-out is fine.
  const counts = useQueries({
    queries: (projects ?? []).map((p) => ({
      queryKey: keys.databases(p.name),
      queryFn: () => api.listDatabases(p.name),
      staleTime: 60_000,
    })),
  })

  const form = useForm<z.infer<typeof schema>>({
    resolver: zodResolver(schema),
    defaultValues: { name: '' },
  })

  return (
    <div className="space-y-6">
      <PageHeader
        title="Projects"
        description="Each project groups the databases for one application."
        actions={
          <Button
            onClick={() => {
              form.reset({ name: '' })
              setCreateOpen(true)
            }}
          >
            <Plus className="size-4" />
            New project
          </Button>
        }
      />

      {error ? (
        <Card>
          <ErrorState error={error} onRetry={() => refetch()} />
        </Card>
      ) : isPending ? (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {Array.from({ length: 3 }).map((_, i) => (
            <Card key={i}>
              <CardContent className="space-y-3">
                <Skeleton className="h-5 w-32" />
                <Skeleton className="h-4 w-24" />
              </CardContent>
            </Card>
          ))}
        </div>
      ) : projects.length === 0 ? (
        <Card>
          <EmptyState
            icon={FolderPlus}
            title="No projects yet"
            description="A project is a namespace for databases — one per application."
            action={
              <Button onClick={() => setCreateOpen(true)}>
                <Plus className="size-4" />
                Create your first project
              </Button>
            }
          />
        </Card>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {projects.map((p, i) => {
            const q = counts[i]
            return (
              <Card
                key={p.name}
                className="hover:border-primary/40 group relative transition-colors"
              >
                <CardContent className="flex items-start justify-between gap-2">
                  <Link
                    to={`/projects/${encodeURIComponent(p.name)}`}
                    className="min-w-0 flex-1 rounded after:absolute after:inset-0 after:content-['']"
                  >
                    <div className="flex items-center gap-2">
                      <Database className="text-muted-foreground size-4 shrink-0" />
                      <span className="truncate font-mono text-sm font-medium">{p.name}</span>
                    </div>
                    <div className="text-muted-foreground mt-2 flex items-center gap-2 text-xs">
                      {q?.isPending ? (
                        <Skeleton className="h-3 w-16" />
                      ) : q?.isError ? null : (
                        <span className="tabular">
                          {q?.data?.length ?? 0} database{q?.data?.length === 1 ? '' : 's'}
                        </span>
                      )}
                      <span aria-hidden>·</span>
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <span>created {relativePast(p.created_at)}</span>
                        </TooltipTrigger>
                        <TooltipContent>{fmtDate(p.created_at)}</TooltipContent>
                      </Tooltip>
                    </div>
                  </Link>

                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="relative z-10 -mt-1 -mr-1 shrink-0"
                        aria-label={`Actions for ${p.name}`}
                      >
                        <MoreHorizontal className="size-4" />
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end">
                      <DropdownMenuItem
                        variant="destructive"
                        onSelect={() => setToDelete(p.name)}
                      >
                        <Trash2 className="size-4" />
                        Delete project
                      </DropdownMenuItem>
                    </DropdownMenuContent>
                  </DropdownMenu>
                </CardContent>
              </Card>
            )
          })}
        </div>
      )}

      {/* create */}
      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>New project</DialogTitle>
            <DialogDescription>
              The name becomes the prefix of every database in it.
            </DialogDescription>
          </DialogHeader>
          <Form {...form}>
            <form
              id="create-project"
              onSubmit={form.handleSubmit((v) =>
                createProject.mutate(v.name, {
                  onSuccess: () => {
                    toast.success(`Project ${v.name} created`)
                    setCreateOpen(false)
                  },
                  onError: (err) =>
                    form.setError('root', {
                      message: err instanceof Error ? err.message : 'Failed',
                    }),
                }),
              )}
              className="space-y-4"
              noValidate
            >
              <FormField
                control={form.control}
                name="name"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Name</FormLabel>
                    <FormControl>
                      <Input
                        autoFocus
                        autoComplete="off"
                        spellCheck={false}
                        placeholder="myapp"
                        className="font-mono"
                        {...field}
                      />
                    </FormControl>
                    <FormDescription>
                      Lowercase letters, digits and underscores. 2–32 characters.
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              {form.formState.errors.root && (
                <Alert variant="destructive">
                  <AlertDescription>{form.formState.errors.root.message}</AlertDescription>
                </Alert>
              )}
            </form>
          </Form>
          <DialogFooter>
            <Button variant="outline" onClick={() => setCreateOpen(false)}>
              Cancel
            </Button>
            <Button type="submit" form="create-project" disabled={createProject.isPending}>
              {createProject.isPending && <Loader2 className="size-4 animate-spin" />}
              Create project
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* delete — drops every database in the project, so it earns the strongest
          confirmation in the app. */}
      <ConfirmDialog
        open={toDelete !== null}
        onOpenChange={(o) => !o && setToDelete(null)}
        title={`Delete project ${toDelete}?`}
        description={
          <>
            <p>
              This drops <strong>every database</strong> in the project along with their roles.
              The data cannot be recovered.
            </p>
          </>
        }
        confirmPhrase={toDelete ?? undefined}
        confirmLabel="Delete project"
        pending={deleteProject.isPending}
        error={deleteProject.error instanceof Error ? deleteProject.error.message : null}
        onConfirm={() =>
          toDelete &&
          deleteProject.mutate(toDelete, {
            onSuccess: () => {
              toast.success(`Deleted ${toDelete}`)
              setToDelete(null)
            },
          })
        }
      />
    </div>
  )
}
