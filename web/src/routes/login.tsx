import { useEffect } from 'react'
import { useLocation, useNavigate, useSearchParams } from 'react-router'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Info, Loader2, Terminal } from 'lucide-react'
import { api, ApiError } from '@/lib/api'
import { keys, resetExpiredNotice } from '@/lib/query'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Alert, AlertDescription } from '@/components/ui/alert'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { HealthDot } from '@/components/health-dot'
import { ThemeToggle } from '@/components/theme-toggle'

const schema = z.object({
  email: z.string().min(1, 'Email is required'),
  password: z.string().min(1, 'Password is required'),
})

type Values = z.infer<typeof schema>

export function LoginPage() {
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const location = useLocation()
  const qc = useQueryClient()

  const next = params.get('next')
  const notice = (location.state as { notice?: string } | null)?.notice

  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { email: '', password: '' },
  })

  useEffect(() => {
    form.setFocus('email')
  }, [form])

  const login = useMutation({
    // Bad credentials come back as 401; that is this form's business, not a
    // signal that a session ended.
    meta: { skipAuthRedirect: true },
    mutationFn: (v: Values) => api.login(v.email, v.password),
    onSuccess: async () => {
      resetExpiredNotice()
      // Fetch the principal before navigating so the shell never renders a
      // frame with no identity.
      await qc.fetchQuery({ queryKey: keys.whoami, queryFn: api.whoami })
      navigate(next || '/projects', { replace: true })
    },
    onError: (err) => {
      const message =
        err instanceof ApiError && err.status === 429
          ? 'Too many attempts. Wait a minute and try again.'
          : err instanceof Error
            ? err.message
            : 'Sign-in failed'
      form.setError('root', { message })
      form.resetField('password')
      form.setFocus('password')
    },
  })

  return (
    <div className="flex min-h-dvh flex-col">
      <div className="flex flex-1 items-center justify-center px-4 py-12">
        <div className="w-full max-w-sm space-y-6">
          <div className="space-y-2 text-center">
            <div className="bg-primary/10 text-primary mx-auto flex size-11 items-center justify-center rounded-xl">
              <Terminal className="size-5" />
            </div>
            <h1 className="font-mono text-lg font-semibold tracking-tight">pgmanager</h1>
            <p className="text-muted-foreground text-sm">Sign in to the admin console</p>
          </div>

          {notice && (
            <Alert>
              <Info />
              <AlertDescription>{notice}</AlertDescription>
            </Alert>
          )}

          <div className="bg-card shadow-subtle rounded-xl border p-6">
            <Form {...form}>
              <form
                onSubmit={form.handleSubmit((v) => login.mutate(v))}
                className="space-y-4"
                noValidate
              >
                <FormField
                  control={form.control}
                  name="email"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Email</FormLabel>
                      <FormControl>
                        <Input
                          type="email"
                          autoComplete="username"
                          autoCapitalize="none"
                          spellCheck={false}
                          placeholder="you@example.com"
                          {...field}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name="password"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Password</FormLabel>
                      <FormControl>
                        <Input type="password" autoComplete="current-password" {...field} />
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

                <Button type="submit" className="w-full" disabled={login.isPending}>
                  {login.isPending && <Loader2 className="size-4 animate-spin" />}
                  Sign in
                </Button>
              </form>
            </Form>
          </div>

          <p className="text-muted-foreground text-center text-xs leading-relaxed">
            Accounts are created on the server with{' '}
            <code className="bg-muted text-foreground rounded px-1 py-0.5 font-mono">
              pgmanager users add
            </code>
            . There is no sign-up.
          </p>
        </div>
      </div>

      {/* Health is anonymous, so "unreachable" is distinguishable from "wrong
          password" before you even type. */}
      <div className="flex items-center justify-between px-4 py-3">
        <HealthDot />
        <ThemeToggle />
      </div>
    </div>
  )
}
