import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { toast } from 'sonner'
import {
  Copy,
  DatabaseZap,
  Loader2,
  MoreHorizontal,
  Plus,
  Table2,
  Trash2,
  X,
} from 'lucide-react'
import { api } from '@/lib/api'
import { ENVIRONMENTS } from '@/lib/types'
import type { DatabaseInfo, DatabaseSecret } from '@/lib/types'
import { envSegment, fmtDate, isExpired, relativeExpiry, relativePast } from '@/lib/format'
import { useCreateDatabase, useDatabases, useDeleteDatabase, useDeleteProject } from '@/hooks/queries'
import { toastError } from '@/lib/query'
import { PageHeader } from '@/components/page-header'
import { DataTable, type ColumnDef } from '@/components/data-table'
import { EmptyState, ErrorState } from '@/components/states'
import { EnvBadge } from '@/components/env-badge'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { SecretDialog } from '@/components/secret-dialog'
import { copyText, CopyInline } from '@/components/copy-field'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
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
  DropdownMenuSeparator,
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'

const SUGGESTED_EXTENSIONS = ['uuid-ossp', 'pgcrypto', 'postgis', 'vector', 'citext', 'hstore']

const schema = z
  .object({
    env: z.enum(ENVIRONMENTS),
    pr_number: z.string().optional(),
    extensions: z.array(z.string()),
  })
  .refine((v) => v.env !== 'pr' || /^\d+$/.test(v.pr_number ?? ''), {
    message: 'A PR number is required',
    path: ['pr_number'],
  })

type Values = z.infer<typeof schema>

export function ProjectDetailPage() {
  const { name = '' } = useParams()
  const navigate = useNavigate()

  const { data: databases, isPending, error, refetch } = useDatabases(name)
  const createDatabase = useCreateDatabase(name)
  const deleteDatabase = useDeleteDatabase(name)
  const deleteProject = useDeleteProject()

  const [createOpen, setCreateOpen] = useState(false)
  const [extensionDraft, setExtensionDraft] = useState('')
  const [created, setCreated] = useState<DatabaseSecret | null>(null)
  const [toDelete, setToDelete] = useState<DatabaseInfo | null>(null)
  const [deleteProjectOpen, setDeleteProjectOpen] = useState(false)

  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { env: 'dev', pr_number: '', extensions: [] },
  })
  const env = form.watch('env')
  const extensions = form.watch('extensions')

  const addExtension = (raw: string) => {
    const value = raw.trim().replace(/,$/, '')
    if (!value) return
    if (!extensions.includes(value)) form.setValue('extensions', [...extensions, value])
    setExtensionDraft('')
  }

  const openCreate = () => {
    form.reset({ env: 'dev', pr_number: '', extensions: [] })
    setExtensionDraft('')
    setCreateOpen(true)
  }

  const copyConnectionString = async (db: DatabaseInfo) => {
    try {
      const full = await api.credentials(name, envSegment(db))
      if (await copyText(full.connection_string)) toast.success('Connection string copied')
    } catch (err) {
      toastError(err)
    }
  }

  const columns: ColumnDef<DatabaseInfo>[] = [
    {
      key: 'database',
      header: 'Database',
      cell: (db) => (
        <span className="font-mono text-sm font-medium">{db.database_name}</span>
      ),
    },
    { key: 'env', header: 'Environment', cell: (db) => <EnvBadge db={db} /> },
    {
      key: 'user',
      header: 'Role',
      cell: (db) => (
        <CopyInline value={db.user_name} className="text-muted-foreground text-xs" label="role" />
      ),
    },
    {
      key: 'created',
      header: 'Created',
      cell: (db) => (
        <Tooltip>
          <TooltipTrigger asChild>
            <span className="text-muted-foreground text-xs">{relativePast(db.created_at)}</span>
          </TooltipTrigger>
          <TooltipContent>{fmtDate(db.created_at)}</TooltipContent>
        </Tooltip>
      ),
    },
    {
      key: 'expires',
      header: 'Expires',
      cell: (db) =>
        !db.expires_at ? (
          <span className="text-muted-foreground text-xs">never</span>
        ) : isExpired(db.expires_at) ? (
          <Badge variant="destructive" className="text-[11px]">
            expired
          </Badge>
        ) : (
          <Tooltip>
            <TooltipTrigger asChild>
              <span className="text-muted-foreground text-xs">
                {relativeExpiry(db.expires_at)}
              </span>
            </TooltipTrigger>
            <TooltipContent>{fmtDate(db.expires_at)}</TooltipContent>
          </Tooltip>
        ),
    },
    {
      key: 'actions',
      header: '',
      actions: true,
      cell: (db) => (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              aria-label={`Actions for ${db.database_name}`}
            >
              <MoreHorizontal className="size-4" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-52">
            <DropdownMenuItem
              onSelect={() =>
                navigate(
                  `/projects/${encodeURIComponent(name)}/databases/${encodeURIComponent(envSegment(db))}`,
                )
              }
            >
              <DatabaseZap className="size-4" />
              Open
            </DropdownMenuItem>
            <DropdownMenuItem
              onSelect={() =>
                navigate(
                  `/projects/${encodeURIComponent(name)}/databases/${encodeURIComponent(envSegment(db))}/explore`,
                )
              }
            >
              <Table2 className="size-4" />
              Browse data
            </DropdownMenuItem>
            {/* Fetches the secret and copies it without ever rendering it. */}
            <DropdownMenuItem onSelect={() => copyConnectionString(db)}>
              <Copy className="size-4" />
              Copy connection string
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" onSelect={() => setToDelete(db)}>
              <Trash2 className="size-4" />
              Delete database
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      ),
    },
  ]

  return (
    <div className="space-y-6">
      <PageHeader
        title={name}
        mono
        description={
          databases
            ? `${databases.length} database${databases.length === 1 ? '' : 's'}`
            : undefined
        }
        actions={
          <>
            <Button onClick={openCreate}>
              <Plus className="size-4" />
              New database
            </Button>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="outline" size="icon" aria-label="Project actions">
                  <MoreHorizontal className="size-4" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem
                  variant="destructive"
                  onSelect={() => setDeleteProjectOpen(true)}
                >
                  <Trash2 className="size-4" />
                  Delete project
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </>
        }
      />

      <Card className="overflow-hidden py-0">
        {error ? (
          <ErrorState error={error} onRetry={() => refetch()} />
        ) : (
          <DataTable
            columns={columns}
            rows={databases}
            rowKey={(db) => db.database_name}
            loading={isPending}
            onRowClick={(db) =>
              navigate(
                `/projects/${encodeURIComponent(name)}/databases/${encodeURIComponent(envSegment(db))}`,
              )
            }
            empty={
              <EmptyState
                icon={DatabaseZap}
                title="No databases yet"
                description="Create one per environment — dev, staging, prod, or a PR database with a TTL."
                action={
                  <Button onClick={openCreate}>
                    <Plus className="size-4" />
                    New database
                  </Button>
                }
              />
            }
            className="md:[&_table]:border-0"
          />
        )}
      </Card>

      {/* create database */}
      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>New database</DialogTitle>
            <DialogDescription>
              Creates the database, a dedicated role, and its password.
            </DialogDescription>
          </DialogHeader>
          <Form {...form}>
            <form
              id="create-database"
              className="space-y-4"
              noValidate
              onSubmit={form.handleSubmit((v) =>
                createDatabase.mutate(
                  {
                    env: v.env,
                    pr_number: v.env === 'pr' ? Number(v.pr_number) : undefined,
                    extensions: v.extensions.length ? v.extensions : undefined,
                  },
                  {
                    onSuccess: (db) => {
                      setCreateOpen(false)
                      setCreated(db) // password is returned exactly once
                    },
                    onError: (err) =>
                      form.setError('root', {
                        message: err instanceof Error ? err.message : 'Failed',
                      }),
                  },
                ),
              )}
            >
              <FormField
                control={form.control}
                name="env"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Environment</FormLabel>
                    <Select value={field.value} onValueChange={field.onChange}>
                      <FormControl>
                        <SelectTrigger className="w-full">
                          <SelectValue />
                        </SelectTrigger>
                      </FormControl>
                      <SelectContent>
                        {ENVIRONMENTS.map((e) => (
                          <SelectItem key={e} value={e} className="font-mono">
                            {e}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                    <FormMessage />
                  </FormItem>
                )}
              />

              {env === 'pr' && (
                <FormField
                  control={form.control}
                  name="pr_number"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>PR number</FormLabel>
                      <FormControl>
                        <Input
                          type="number"
                          min={1}
                          inputMode="numeric"
                          className="font-mono"
                          placeholder="42"
                          {...field}
                        />
                      </FormControl>
                      <FormDescription>
                        PR databases expire automatically and are removed by cleanup.
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              )}

              <FormField
                control={form.control}
                name="extensions"
                render={() => (
                  <FormItem>
                    <FormLabel>Extensions</FormLabel>
                    <FormControl>
                      <div className="space-y-2">
                        <div className="border-input focus-within:border-ring flex min-h-9 flex-wrap items-center gap-1.5 rounded-md border px-2 py-1.5 transition-colors">
                          {extensions.map((ext) => (
                            <Badge key={ext} variant="secondary" className="gap-1 font-mono">
                              {ext}
                              <button
                                type="button"
                                aria-label={`Remove ${ext}`}
                                onClick={() =>
                                  form.setValue(
                                    'extensions',
                                    extensions.filter((e) => e !== ext),
                                  )
                                }
                                className="hover:text-destructive rounded"
                              >
                                <X className="size-3" />
                              </button>
                            </Badge>
                          ))}
                          <input
                            value={extensionDraft}
                            onChange={(e) => {
                              const v = e.target.value
                              if (v.endsWith(',')) addExtension(v)
                              else setExtensionDraft(v)
                            }}
                            onKeyDown={(e) => {
                              if (e.key === 'Enter') {
                                e.preventDefault()
                                addExtension(extensionDraft)
                              } else if (
                                e.key === 'Backspace' &&
                                !extensionDraft &&
                                extensions.length
                              ) {
                                form.setValue('extensions', extensions.slice(0, -1))
                              }
                            }}
                            onBlur={() => addExtension(extensionDraft)}
                            placeholder={extensions.length ? '' : 'type and press Enter'}
                            className="placeholder:text-muted-foreground min-w-[8rem] flex-1 bg-transparent font-mono text-sm outline-none"
                            aria-label="Add an extension"
                          />
                        </div>
                        <div className="flex flex-wrap gap-1.5">
                          {SUGGESTED_EXTENSIONS.filter((e) => !extensions.includes(e)).map((e) => (
                            <button
                              key={e}
                              type="button"
                              onClick={() => addExtension(e)}
                              className="text-muted-foreground hover:border-primary/50 hover:text-foreground rounded-full border px-2 py-0.5 font-mono text-[11px] transition-colors"
                            >
                              + {e}
                            </button>
                          ))}
                        </div>
                      </div>
                    </FormControl>
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
            <Button type="submit" form="create-database" disabled={createDatabase.isPending}>
              {createDatabase.isPending && <Loader2 className="size-4 animate-spin" />}
              Create database
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {created && (
        <SecretDialog
          open
          onClose={() => setCreated(null)}
          title={created.database_name}
          warning="These credentials grant full access to the database. Treat them as secrets."
          note="You can retrieve them again from this database's page."
          connectionString={created.connection_string}
          rows={[
            { label: 'Database', value: created.database_name },
            { label: 'Role', value: created.user_name },
            { label: 'Password', value: created.password, secret: true },
            { label: 'Host', value: `${created.host}:${created.port}` },
            { label: 'Connection string', value: created.connection_string, secret: true },
          ]}
        />
      )}

      <ConfirmDialog
        open={toDelete !== null}
        onOpenChange={(o) => !o && setToDelete(null)}
        title={`Delete ${toDelete?.database_name}?`}
        description={
          <p>
            The database and its role are dropped immediately. The data cannot be recovered.
          </p>
        }
        confirmPhrase={toDelete?.database_name}
        confirmLabel="Delete database"
        pending={deleteDatabase.isPending}
        error={deleteDatabase.error instanceof Error ? deleteDatabase.error.message : null}
        onConfirm={() =>
          toDelete &&
          deleteDatabase.mutate(envSegment(toDelete), {
            onSuccess: () => {
              toast.success(`Deleted ${toDelete.database_name}`)
              setToDelete(null)
            },
          })
        }
      />

      <ConfirmDialog
        open={deleteProjectOpen}
        onOpenChange={setDeleteProjectOpen}
        title={`Delete project ${name}?`}
        description={
          <p>
            This drops <strong>every database</strong> in the project along with their roles. The
            data cannot be recovered.
          </p>
        }
        confirmPhrase={name}
        confirmLabel="Delete project"
        pending={deleteProject.isPending}
        error={deleteProject.error instanceof Error ? deleteProject.error.message : null}
        onConfirm={() =>
          deleteProject.mutate(name, {
            onSuccess: () => {
              toast.success(`Deleted ${name}`)
              navigate('/projects', { replace: true })
            },
          })
        }
      />

      <p className="text-muted-foreground text-xs">
        <Link to="/projects" className="hover:text-foreground underline underline-offset-2">
          All projects
        </Link>
      </p>
    </div>
  )
}
