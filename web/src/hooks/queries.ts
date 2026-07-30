import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { keys } from '@/lib/query'
import { canManageTokens } from '@/lib/scopes'
import type {
  ApproveDeviceRequest,
  CreateDatabaseRequest,
  CreateTokenRequest,
  Whoami,
} from '@/lib/types'

export function useWhoami() {
  return useQuery({
    queryKey: keys.whoami,
    queryFn: api.whoami,
    retry: false,
    staleTime: 5 * 60_000,
  })
}

export function useHealth() {
  return useQuery({
    queryKey: keys.health,
    queryFn: api.health,
    refetchInterval: 30_000,
    refetchIntervalInBackground: false,
    retry: false,
    // Health is the one query allowed to fail quietly; "unreachable" is a
    // legitimate rendering, not an error state.
    staleTime: 0,
  })
}

export function useProjects() {
  return useQuery({ queryKey: keys.projects, queryFn: api.listProjects })
}

export function useDatabases(project: string, enabled = true) {
  return useQuery({
    queryKey: keys.databases(project),
    queryFn: () => api.listDatabases(project),
    enabled: enabled && Boolean(project),
  })
}

export function useDatabase(project: string, env: string) {
  return useQuery({
    queryKey: keys.database(project, env),
    queryFn: () => api.getDatabase(project, env),
    enabled: Boolean(project && env),
  })
}

/** Credentials are fetched only on demand and never retained: a password should
 *  not sit in the cache because someone opened a page. */
export function useCredentials(project: string, env: string, enabled: boolean) {
  return useQuery({
    queryKey: keys.credentials(project, env),
    queryFn: () => api.credentials(project, env),
    enabled,
    gcTime: 0,
    staleTime: 0,
  })
}

export function useTokens(enabled = true) {
  return useQuery({ queryKey: keys.tokens, queryFn: api.listTokens, enabled })
}

export function useDevices(who: Whoami | undefined) {
  return useQuery({
    queryKey: keys.devices,
    queryFn: api.listDevices,
    enabled: canManageTokens(who),
    // Someone is standing at a terminal waiting for this.
    refetchInterval: 5_000,
    refetchIntervalInBackground: false,
  })
}

export function useTables(project: string, env: string, enabled = true) {
  return useQuery({
    queryKey: keys.tables(project, env),
    queryFn: async () => (await api.listTables(project, env)).tables ?? [],
    enabled: enabled && Boolean(project && env),
  })
}

/* ------------------------------------------------------------- mutations */

export function useCreateProject() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (name: string) => api.createProject(name),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.projects }),
  })
}

export function useDeleteProject() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (name: string) => api.deleteProject(name),
    onSuccess: (_data, name) => {
      qc.invalidateQueries({ queryKey: keys.projects })
      qc.removeQueries({ queryKey: keys.databases(name) })
    },
  })
}

export function useCreateDatabase(project: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: CreateDatabaseRequest) => api.createDatabase(project, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.databases(project) }),
  })
}

export function useDeleteDatabase(project: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (env: string) => api.deleteDatabase(project, env),
    onSuccess: (_data, env) => {
      qc.invalidateQueries({ queryKey: keys.databases(project) })
      qc.removeQueries({ queryKey: keys.database(project, env) })
    },
  })
}

export function useRotatePassword(project: string, env: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (terminate: boolean) => api.rotatePassword(project, env, terminate),
    onSuccess: () => {
      // Deliberately not writing the returned secret into the cache.
      qc.invalidateQueries({ queryKey: keys.database(project, env) })
      qc.invalidateQueries({ queryKey: keys.databases(project) })
      qc.removeQueries({ queryKey: keys.credentials(project, env) })
    },
  })
}

export function useCreateToken() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: CreateTokenRequest) => api.createToken(body),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.tokens }),
  })
}

export function useRevokeToken() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (prefix: string) => api.revokeToken(prefix),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.tokens }),
  })
}

export function useApproveDevice() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ code, body }: { code: string; body: ApproveDeviceRequest }) =>
      api.approveDevice(code, body),
    onSuccess: () => {
      // Approving mints a token, so both lists move.
      qc.invalidateQueries({ queryKey: keys.devices })
      qc.invalidateQueries({ queryKey: keys.tokens })
    },
  })
}

export function useDenyDevice() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (code: string) => api.denyDevice(code),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.devices }),
  })
}

export function useCleanup() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (olderThan: string) => api.cleanup(olderThan),
    onSuccess: () => {
      // Any project's PR databases may have gone.
      qc.invalidateQueries({ queryKey: ['databases'] })
    },
  })
}
