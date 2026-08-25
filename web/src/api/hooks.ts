import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiDelete, apiGet, apiPost, apiPut } from './client'
import type { LogItem, Mapping, MetricPoint, Node, Overview, PortCheckResult, Settings, TargetInput } from './types'

export const qk = {
  me: ['me'] as const,
  overview: ['overview'] as const,
  nodes: ['nodes'] as const,
  node: (id: number) => ['node', id] as const,
  nodeHistory: (id: number, hours: number) => ['nodeHistory', id, hours] as const,
  mappings: ['mappings'] as const,
  logs: (p: Record<string, unknown>) => ['logs', p] as const,
  settings: ['settings'] as const,
}

export interface Me { username: string; panel_name: string; version: string; install_base: string }

export function useMe() {
  return useQuery({ queryKey: qk.me, queryFn: () => apiGet<Me>('/me'), retry: false, staleTime: 60000 })
}
export function useOverview() {
  return useQuery({ queryKey: qk.overview, queryFn: () => apiGet<Overview>('/overview'), refetchInterval: 15000 })
}
export function useNodes() {
  return useQuery({ queryKey: qk.nodes, queryFn: () => apiGet<Node[]>('/nodes'), refetchInterval: 15000 })
}
export function useNode(id: number, enabled = true) {
  return useQuery({ queryKey: qk.node(id), queryFn: () => apiGet<Node>(`/nodes/${id}`), enabled })
}
export function useNodeHistory(id: number, hours: number, enabled = true) {
  return useQuery({
    queryKey: qk.nodeHistory(id, hours),
    queryFn: () => apiGet<{ points: MetricPoint[]; traffic: unknown[] }>(`/nodes/${id}/history?hours=${hours}`),
    enabled,
  })
}
export function useMappings() {
  return useQuery({ queryKey: qk.mappings, queryFn: () => apiGet<Mapping[]>('/mappings'), refetchInterval: 15000 })
}
export function useLogs(params: { type?: string; node_id?: number; page: number; size: number }) {
  const q = new URLSearchParams()
  if (params.type) q.set('type', params.type)
  if (params.node_id) q.set('node_id', String(params.node_id))
  q.set('page', String(params.page))
  q.set('size', String(params.size))
  return useQuery({
    queryKey: qk.logs(params),
    queryFn: () => apiGet<{ items: LogItem[]; total: number; page: number; size: number }>(`/logs?${q}`),
  })
}
export function useSettings() {
  return useQuery({ queryKey: qk.settings, queryFn: () => apiGet<Settings>('/settings') })
}

// ----- mutations -----
export function useLogin() {
  return useMutation({ mutationFn: (b: { username: string; password: string }) => apiPost('/login', b) })
}
export function useLogout() {
  const qc = useQueryClient()
  return useMutation({ mutationFn: () => apiPost('/logout'), onSuccess: () => qc.clear() })
}
export function useCreateNode() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (b: { name: string; region: string; receipt: string }) => apiPost('/nodes', b),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.nodes }),
  })
}
export function useUpdateNode() {
  const qc = useQueryClient()
  return useMutation({
    // id goes in the path only: the panel decodes with DisallowUnknownFields,
    // so leaving it in the body makes every edit fail as a malformed request.
    mutationFn: ({ id, ...body }: { id: number; name: string; region: string }) => apiPut(`/nodes/${id}`, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.nodes }),
  })
}
export function useDeleteNode() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => apiDelete(`/nodes/${id}`),
    onSuccess: () => { qc.invalidateQueries({ queryKey: qk.nodes }); qc.invalidateQueries({ queryKey: qk.mappings }) },
  })
}
export function useRotateToken() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => apiPost(`/nodes/${id}/rotate-token`),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.nodes }),
  })
}
export function useUpdateAgent() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => apiPost<{ started: boolean; target: string }>(`/nodes/${id}/update-agent`),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.nodes }),
  })
}
export function useSelfUpdate() {
  return useMutation({ mutationFn: () => apiPost<{ restarting: boolean }>('/settings/self-update') })
}
export function useCreateMapping() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (b: MappingBody) => apiPost('/mappings', b),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.mappings }),
  })
}
export function useUpdateMapping() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...body }: MappingBody & { id: number }) => apiPut(`/mappings/${id}`, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.mappings }),
  })
}
export function useDeleteMapping() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => apiDelete(`/mappings/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.mappings }),
  })
}
export function useToggleMapping() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...body }: { id: number; enabled: boolean; version: number }) => apiPost(`/mappings/${id}/toggle`, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.mappings }),
  })
}
export function useChangePassword() {
  return useMutation({ mutationFn: (b: { old_password: string; new_password: string }) => apiPost('/settings/password', b) })
}
export function useUpdateSettings() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (b: Partial<{ panel_name: string; conn_rate_limit: number; tcp_ping_interval: number; auto_backup: boolean; update_mirror: string }>) => apiPut('/settings', b),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.settings }),
  })
}
export function useCleanLogs() {
  const qc = useQueryClient()
  return useMutation({ mutationFn: (all: boolean) => apiPost('/logs/clean', { all }), onSuccess: () => qc.invalidateQueries({ queryKey: ['logs'] }) })
}
export function useBackup() {
  return useMutation({ mutationFn: () => apiPost<{ file: string; size: number }>('/settings/backup') })
}
export function useCheckUpdate() {
  return useMutation({ mutationFn: () => apiGet<{ current: string; latest: string; has_update: boolean; upgrade_cmd?: string; message?: string }>('/settings/check-update') })
}

export interface MappingBody {
  local_port: number
  proto: string
  remark: string
  enabled: boolean
  version: number
  targets: TargetInput[]
}

// Imperative port checks (used by the mapping dialog with debounce).
export function portCheck(body: { node_id: number; port: number; proto: string }) {
  return apiPost<PortCheckResult>('/mappings/port-check', body)
}
export function localCheck(body: { port: number; proto: string }) {
  return apiPost<{ listening: boolean; process: string }>('/mappings/local-check', body)
}
