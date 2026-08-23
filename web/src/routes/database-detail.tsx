import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router'
import { toast } from 'sonner'
import { AlertTriangle, Clock, Eye, KeyRound, Loader2, Table2, Trash2 } from 'lucide-react'
import {
  useCredentials,
  useDatabase,
  useDeleteDatabase,
  useTables,
} from '@/hooks/queries'
import { envSegment, expiringSoon, fmtDate, isExpired, relativeExpiry } from '@/lib/format'
import { PageHeader } from '@/components/page-header'
import { EmptyState, ErrorState } from '@/components/states'
import { EnvBadge } from '@/components/env-badge'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { SecretDialog } from '@/components/secret-dialog'
import { RotateDialog, RotateTrigger } from '@/components/rotate-dialog'
import { BackupsCard } from '@/components/backups-card'
import { CopyField, CopyInline } from '@/components/copy-field'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'

export function DatabaseDetailPage() {
  const { name = '', env = '' } = useParams()
  const navigate = useNavigate()

  const { data: db, isPending, error, refetch } = useDatabase(name, env)
  const { data: tables, error: tablesError, isPending: tablesPending } = useTables(name, env)
  const deleteDatabase = useDeleteDatabase(name)

  // Nothing fetches a secret until the operator asks for one.
  const [wantCredentials, setWantCredentials] = useState(false)
  const [showSecret, setShowSecret] = useState(false)
  const [rotateOpen, setRotateOpen] = useState(false)
  const [deleteOpen, setDeleteOpen] = useState(false)

  const credentials = useCredentials(name, env, wantCredentials)
  const explorePath = `/projects/${encodeURIComponent(name)}/databases/${encodeURIComponent(env)}/explore`

  if (isPending) return <DetailSkeleton />

  if (error) {
    return (
      <Card>
        <ErrorState error={error} onRetry={() => refetch()} />
      </Card>
    )
  }

  if (!db) {
    return (
      <Card>
        <EmptyState
          title="That database doesn't exist"
          description="It may have been deleted or cleaned up."
          action={
            <Button asChild variant="outline">
              <Link to={`/projects/${encodeURIComponent(name)}`}>Back to {name}</Link>
            </Button>
          }
        />
      </Card>
    )
  }

  const expired = isExpired(db.expires_at)
  const soon = expiringSoon(db.expires_at)
  const conn = credentials.data

  return (
    <div className="space-y-6">
      <PageHeader
        title={db.database_name}
        mono
        description={
          <span className="flex items-center gap-2">
            <EnvBadge db={db} />
            <span>created {fmtDate(db.created_at)}</span>
          </span>
        }
        actions={
          <Button asChild>
            <Link to={explorePath}>
              <Table2 className="size-4" />
              Browse data
            </Link>
          </Button>
        }
      />

      {(expired || soon) && (
        <Alert variant={expired ? 'destructive' : 'default'}>
          <Clock />
          <AlertTitle>{expired ? 'This database has expired' : 'Expiring soon'}</AlertTitle>
          <AlertDescription>
            {expired
              ? `It expired ${fmtDate(db.expires_at)} and will be removed by the next cleanup run.`
              : `It expires ${relativeExpiry(db.expires_at)} (${fmtDate(db.expires_at)}).`}
          </AlertDescription>
        </Alert>
      )}

      <div className="grid gap-6 lg:grid-cols-3">
        <div className="space-y-6 lg:col-span-2">
          <Card>
            <CardHeader>
              <CardTitle className="text-sm">Overview</CardTitle>
            </CardHeader>
            <CardContent>
              <dl className="grid gap-x-6 gap-y-4 sm:grid-cols-2">
                <Field label="Database">
                  <CopyInline value={db.database_name} label="database name" />
                </Field>
                <Field label="Role">
                  <CopyInline value={db.user_name} label="role" />
                </Field>
                <Field label="Host">
                  <CopyInline value={db.host} label="host" />
                </Field>
                <Field label="Port">
                  <span className="font-mono tabular">{db.port}</span>
                </Field>
                <Field label="Environment">
                  <EnvBadge db={db} />
                </Field>
                <Field label="Expires">
                  {db.expires_at ? (
                    <Tooltip>
                      <TooltipTrigger asChild>
                        <span className={expired ? 'text-destructive' : undefined}>
                          {relativeExpiry(db.expires_at)}
                        </span>
                      </TooltipTrigger>
                      <TooltipContent>{fmtDate(db.expires_at)}</TooltipContent>
                    </Tooltip>
                  ) : (
                    <span className="text-muted-foreground">never</span>
                  )}
                </Field>
              </dl>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="text-sm">Connect</CardTitle>
            </CardHeader>
            <CardContent>
              {!wantCredentials ? (
                <div className="flex flex-col items-start gap-3">
                  <p className="text-muted-foreground text-sm">
                    The connection string embeds the password, so it is fetched only when you ask
                    for it.
                  </p>
                  <Button variant="outline" onClick={() => setWantCredentials(true)}>
                    <Eye className="size-4" />
                    Show connection details
                  </Button>
                </div>
              ) : credentials.isPending ? (
                <div className="space-y-3">
                  <Skeleton className="h-9 w-64" />
                  <Skeleton className="h-9 w-full" />
                </div>
              ) : credentials.error ? (
                <ErrorState error={credentials.error} onRetry={() => credentials.refetch()} />
              ) : conn ? (
                <Tabs defaultValue="uri">
                  <TabsList>
                    <TabsTrigger value="uri">URI</TabsTrigger>
                    <TabsTrigger value="psql">psql</TabsTrigger>
                    <TabsTrigger value="env">.env</TabsTrigger>
                    <TabsTrigger value="node">Node</TabsTrigger>
                  </TabsList>
                  <TabsContent value="uri" className="pt-4">
                    <CopyField
                      label="Connection string"
                      value={conn.connection_string}
                      secret
                      name="connection string"
                    />
                  </TabsContent>
                  <TabsContent value="psql" className="pt-4">
                    <CopyField
                      label="Command"
                      value={`psql "${conn.connection_string}"`}
                      secret
                      name="psql command"
                    />
                  </TabsContent>
                  <TabsContent value="env" className="pt-4">
                    <CopyField
                      label=".env line"
                      value={`DATABASE_URL="${conn.connection_string}"`}
                      secret
                      name="env line"
                    />
                  </TabsContent>
                  <TabsContent value="node" className="pt-4">
                    <CopyField
                      label="pg client"
                      value={`new Client({ connectionString: process.env.DATABASE_URL })`}
                      name="node snippet"
                    />
                  </TabsContent>
                </Tabs>
              ) : null}
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="text-sm">Tables</CardTitle>
            </CardHeader>
            <CardContent>
              {tablesPending ? (
                <div className="flex flex-wrap gap-2">
                  {Array.from({ length: 5 }).map((_, i) => (
                    <Skeleton key={i} className="h-6 w-20" />
                  ))}
                </div>
              ) : tablesError ? (
                // A broken explorer connection must not take the page down.
                <Alert variant="destructive">
                  <AlertTriangle />
                  <AlertDescription>
                    {tablesError instanceof Error
                      ? tablesError.message
                      : 'Could not list tables'}
                  </AlertDescription>
                </Alert>
              ) : !tables || tables.length === 0 ? (
                <p className="text-muted-foreground text-sm">
                  No tables yet. Run your migrations against this database and they will show up
                  here.
                </p>
              ) : (
                <div className="space-y-3">
                  <p className="text-muted-foreground text-sm tabular">
                    {tables.length} table{tables.length === 1 ? '' : 's'}
                  </p>
                  <div className="flex flex-wrap gap-1.5">
                    {tables.slice(0, 8).map((t) => (
                      <Link
                        key={`${t.schema}.${t.name}`}
                        to={`${explorePath}?schema=${encodeURIComponent(t.schema)}&table=${encodeURIComponent(t.name)}`}
                      >
                        <Badge
                          variant="outline"
                          className="hover:border-primary/50 hover:text-foreground font-mono transition-colors"
                        >
                          {t.schema === 'public' ? t.name : `${t.schema}.${t.name}`}
                        </Badge>
                      </Link>
                    ))}
                  </div>
                  <Link
                    to={explorePath}
                    className="text-primary inline-block text-sm underline-offset-4 hover:underline"
                  >
                    Browse all {tables.length} tables →
                  </Link>
                </div>
              )}
            </CardContent>
          </Card>

          {/* Backups don't exist for pr databases — they're throwaway by
              design, and the server rejects every backup route for env=pr. */}
          {db.env !== 'pr' && <BackupsCard db={db} />}
        </div>

        <div className="lg:col-span-1">
          <Card className="lg:sticky lg:top-28">
            <CardHeader>
              <CardTitle className="text-sm">Actions</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              <Button asChild variant="outline" className="w-full justify-start">
                <Link to={explorePath}>
                  <Table2 className="size-4" />
                  Open in explorer
                </Link>
              </Button>

              <Button
                variant="outline"
                className="w-full justify-start"
                onClick={() => {
                  setWantCredentials(true)
                  setShowSecret(true)
                }}
                disabled={credentials.isFetching && wantCredentials}
              >
                {credentials.isFetching && showSecret ? (
                  <Loader2 className="size-4 animate-spin" />
                ) : (
                  <KeyRound className="size-4" />
                )}
                Show credentials
              </Button>

              <RotateTrigger db={db} onClick={() => setRotateOpen(true)} />

              <Separator />

              <Button
                variant="ghost"
                className="text-destructive hover:bg-destructive/10 hover:text-destructive w-full justify-start"
                onClick={() => setDeleteOpen(true)}
              >
                <Trash2 className="size-4" />
                Delete database
              </Button>
            </CardContent>
          </Card>
        </div>
      </div>

      {showSecret && conn && (
        <SecretDialog
          open
          onClose={() => setShowSecret(false)}
          title={conn.database_name}
          warning="These credentials grant full access to the database. Treat them as secrets."
          connectionString={conn.connection_string}
          rows={[
            { label: 'Database', value: conn.database_name },
            { label: 'Role', value: conn.user_name },
            { label: 'Password', value: conn.password, secret: true },
            { label: 'Host', value: `${conn.host}:${conn.port}` },
            { label: 'Connection string', value: conn.connection_string, secret: true },
          ]}
        />
      )}

      <RotateDialog db={db} open={rotateOpen} onOpenChange={setRotateOpen} />

      <ConfirmDialog
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        title={`Delete ${db.database_name}?`}
        description={
          <p>The database and its role are dropped immediately. The data cannot be recovered.</p>
        }
        confirmPhrase={db.database_name}
        confirmLabel="Delete database"
        pending={deleteDatabase.isPending}
        error={deleteDatabase.error instanceof Error ? deleteDatabase.error.message : null}
        onConfirm={() =>
          deleteDatabase.mutate(envSegment(db), {
            onSuccess: () => {
              toast.success(`Deleted ${db.database_name}`)
              navigate(`/projects/${encodeURIComponent(name)}`, { replace: true })
            },
          })
        }
      />
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="min-w-0 space-y-1">
      <dt className="text-muted-foreground text-xs">{label}</dt>
      <dd className="truncate text-sm">{children}</dd>
    </div>
  )
}

function DetailSkeleton() {
  return (
    <div className="space-y-6" aria-busy="true">
      <Skeleton className="h-8 w-64" />
      <div className="grid gap-6 lg:grid-cols-3">
        <div className="space-y-6 lg:col-span-2">
          <Skeleton className="h-52 w-full rounded-xl" />
          <Skeleton className="h-40 w-full rounded-xl" />
        </div>
        <Skeleton className="h-64 w-full rounded-xl" />
      </div>
    </div>
  )
}
