import { launcherFetch } from "@/api/http"

export type Isolation = "machine" | "container" | "profile" | "shared"

export interface ComputeView {
  id: string
  name: string
  kind: "local" | "remote"
  mode?: "dial_out" | "dial_in"
  url?: string
  status: "pending" | "enrolled"
  capabilities: { docker: boolean; docker_version?: string; browser?: boolean }
  isolations: Isolation[]
  compita_count: number
  online: boolean
}

export interface CompitaView {
  id: string
  name: string
  description?: string
  compute_id: string
  isolation: Isolation
  approvals?: ApprovalPreset
  browser?: boolean
}

export type ApprovalPreset = "careful" | "open"

export interface ComputeState {
  enabled: boolean
  computes: ComputeView[]
  compitas: CompitaView[]
}

export interface ComputeAdded {
  compute: ComputeView
  token: string
  enroll_command: string
  manual_command?: string
  expires_in_minutes: number
}

async function call<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await launcherFetch(path, {
    ...init,
    headers: { "Content-Type": "application/json", ...init?.headers },
  })
  const body = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw new Error(
      (body as { error?: string }).error ?? `request failed: ${res.status}`,
    )
  }
  return body as T
}

const send = (method: string, body?: unknown): RequestInit => ({
  method,
  body: body === undefined ? undefined : JSON.stringify(body),
})

export const getComputeState = () => call<ComputeState>("/api/compute")

export const setComputeEnabled = (enabled: boolean) =>
  call<ComputeState>("/api/compute/enabled", send("PUT", { enabled }))

export const refreshLocalCompute = () =>
  call<ComputeState>("/api/compute/refresh-local", send("POST"))

export const addCompute = (req: {
  name: string
  mode: "dial_out" | "dial_in"
  url?: string
  pin?: string
}) => call<ComputeAdded>("/api/computes", send("POST", req))

export const reissueComputeToken = (id: string) =>
  call<Omit<ComputeAdded, "compute">>(
    `/api/computes/${encodeURIComponent(id)}/token`,
    send("POST"),
  )

export const removeCompute = (id: string) =>
  call<ComputeState>(`/api/computes/${encodeURIComponent(id)}`, send("DELETE"))

export const addCompita = (req: {
  name: string
  description?: string
  compute_id: string
  isolation: Isolation
  approvals?: ApprovalPreset
  browser?: boolean
}) => call<ComputeState>("/api/compitas", send("POST", req))

export const removeCompita = (id: string) =>
  call<ComputeState>(`/api/compitas/${encodeURIComponent(id)}`, send("DELETE"))

export interface ChatMessage {
  role: "user" | "peer" | "compita" | "objective" | "approval"
  text: string
  error?: boolean
  at: string
}

export const getCompitaChat = (id: string) =>
  call<{ messages: ChatMessage[] }>(
    `/api/compitas/${encodeURIComponent(id)}/chat`,
  ).then((r) => r.messages)

export const sendCompitaMessage = (id: string, message: string) =>
  call<ChatMessage>(
    `/api/compitas/${encodeURIComponent(id)}/chat`,
    send("POST", { message }),
  )

export const refreshCompute = (id: string) =>
  call<ComputeState>(
    `/api/computes/${encodeURIComponent(id)}/refresh`,
    send("POST"),
  )

export type ObjectiveStatus =
  "active" | "done" | "blocked" | "paused" | "stopped" | "exhausted"

export interface Objective {
  goal: string
  status: ObjectiveStatus
  turns: number
  max_turns: number
  max_minutes: number
  note?: string
  summary?: string
  reason?: string
  claimed?: string
}

export interface ObjectiveView {
  objective: Objective | null
  running: boolean
  awaiting_approval: number
}

export interface Approval {
  id: string
  compita_id: string
  tool: string
  summary: string
  at: string
}

export const getApprovals = (compita?: string) =>
  call<{ approvals: Approval[] }>(
    `/api/compute/approvals${compita ? `?compita=${encodeURIComponent(compita)}` : ""}`,
  ).then((r) => r.approvals)

export const decideApproval = (id: string, approved: boolean) =>
  call<{ ok: boolean }>(
    `/api/compute/approvals/${encodeURIComponent(id)}`,
    send("POST", { approved }),
  )

const objectiveUrl = (id: string) =>
  `/api/compitas/${encodeURIComponent(id)}/objective`

export const getObjective = (id: string) =>
  call<ObjectiveView>(objectiveUrl(id))

export const startObjective = (id: string, goal: string) =>
  call<ObjectiveView>(objectiveUrl(id), send("POST", { goal }))

export const objectiveAction = (
  id: string,
  action: "pause" | "resume" | "stop",
) => call<ObjectiveView>(`${objectiveUrl(id)}/${action}`, send("POST"))

export interface Schedule {
  id: string
  goal: string
  every_minutes: number
  enabled: boolean
  next_run: string
}

export interface TriggersView {
  schedules: Schedule[]
  trigger: { enabled: boolean; instruction?: string; pending: number }
}

const compitaUrl = (id: string) => `/api/compitas/${encodeURIComponent(id)}`

export const getTriggers = (id: string) =>
  call<TriggersView>(`${compitaUrl(id)}/triggers`)

export const addSchedule = (id: string, goal: string, every_minutes: number) =>
  call<Schedule>(
    `${compitaUrl(id)}/schedules`,
    send("POST", { goal, every_minutes }),
  )

export const removeSchedule = (id: string, scheduleId: string) =>
  call<{ ok: boolean }>(
    `${compitaUrl(id)}/schedules/${encodeURIComponent(scheduleId)}`,
    send("DELETE"),
  )

export const toggleSchedule = (
  id: string,
  scheduleId: string,
  enabled: boolean,
) =>
  call<{ ok: boolean }>(
    `${compitaUrl(id)}/schedules/${encodeURIComponent(scheduleId)}`,
    send("POST", { enabled }),
  )

export const createTrigger = (id: string, instruction: string) =>
  call<{ token: string; url: string }>(
    `${compitaUrl(id)}/trigger/token`,
    send("POST", { instruction }),
  )

export const revokeTrigger = (id: string) =>
  call<{ ok: boolean }>(`${compitaUrl(id)}/trigger`, send("DELETE"))

export interface Connection {
  name: string
  command: string
  args: string[]
  env_keys: string[]
}

export const getConnections = (id: string) =>
  call<{ connections: Connection[] }>(`${compitaUrl(id)}/connections`).then(
    (r) => r.connections,
  )

export const setConnection = (
  id: string,
  name: string,
  body: { command: string; args: string[]; env: Record<string, string> },
) =>
  call<{ connections: Connection[] }>(
    `${compitaUrl(id)}/connections/${encodeURIComponent(name)}`,
    send("PUT", body),
  ).then((r) => r.connections)

export const removeConnection = (id: string, name: string) =>
  call<{ connections: Connection[] }>(
    `${compitaUrl(id)}/connections/${encodeURIComponent(name)}`,
    send("DELETE"),
  ).then((r) => r.connections)
