import { atom, getDefaultStore } from "jotai"
import { atomWithStorage } from "jotai/utils"

import {
  ASSISTANT_DETAIL_VISIBILITY_STORAGE_KEY,
  type AssistantDetailVisibility,
  DEFAULT_ASSISTANT_DETAIL_VISIBILITY,
  assistantDetailVisibilityStorage,
  shouldShowAssistantMessage,
} from "@/features/chat/detail-visibility"
import {
  getInitialActiveSessionId,
  writeStoredSessionId,
} from "@/features/chat/state"

export interface ChatAttachment {
  type: "image" | "audio" | "video" | "file"
  url: string
  filename?: string
  contentType?: string
}

export interface ChatToolCallFunction {
  name?: string
  arguments?: string
}

export interface ChatToolCallExtraContent {
  toolFeedbackExplanation?: string
}

export interface ChatToolCall {
  id?: string
  type?: string
  function?: ChatToolCallFunction
  extraContent?: ChatToolCallExtraContent
}

export type AssistantMessageKind = "normal" | "thought" | "tool_calls"

export interface ChatMessage {
  id: string
  role: "user" | "assistant"
  content: string
  timestamp: number | string
  kind?: AssistantMessageKind
  modelName?: string
  servedTarget?: string
  servedIdentity?: string
  attachments?: ChatAttachment[]
  toolCalls?: ChatToolCall[]
}

export function getChatSelection(sessionId: string): string {
  return getChatState().selectionBySession[sessionId] || ""
}

export function setChatSelection(sessionId: string, selection: string) {
  const normalized = selection.trim()
  updateChatStore((state) => {
    const selectionBySession = { ...state.selectionBySession }
    if (normalized) selectionBySession[sessionId] = normalized
    else delete selectionBySession[sessionId]
    return { selectionBySession }
  })
}

export interface ContextUsage {
  used_tokens: number
  total_tokens: number
  history_tokens?: number
  compress_at_tokens: number
  summarize_at_tokens?: number
  used_percent: number
}

export type ConnectionState =
  "disconnected" | "connecting" | "connected" | "error"

/** A message the server refused after the composer was cleared. */
export interface FailedDraft {
  content: string
  attachments?: ChatAttachment[]
}

export interface ChatQuestionOption {
  label: string
  description: string
  recommended?: boolean
}

export interface ChatQuestionItem {
  header: string
  question: string
  options: ChatQuestionOption[]
  multiple?: boolean
}

/** A question.create the agent is waiting on. */
export interface PendingQuestion {
  questionId: string
  /** Unix milliseconds. */
  expiresAt: number
  questions: ChatQuestionItem[]
  /** The id of the last question.answer or question.cancel sent for it. */
  requestId?: string
  /** The server's refusal of that frame. */
  error?: string
}

export interface ChatAnswer {
  selected: string[]
  text: string
}

export interface ChatStoreState {
  messages: ChatMessage[]
  connectionState: ConnectionState
  isTyping: boolean
  /**
   * A reply is being produced: from sending (or typing.start, or turn.start)
   * until turn.end, or, without turn frames as for a command, typing.stop.
   * isTyping ends at the first streamed chunk; this ends when the reply is
   * complete.
   */
  isTurnActive: boolean
  activeSessionId: string
  hasHydratedActiveSession: boolean
  contextUsage?: ContextUsage
  selectionBySession: Record<string, string>
  /** Text to put back in the composer, set until the chat page takes it. */
  failedDraft?: FailedDraft
  /** Questions waiting on an answer, per session, in arrival order. */
  questionsBySession: Record<string, PendingQuestion[]>
}

type ChatStorePatch = Partial<ChatStoreState>

const DEFAULT_CHAT_STATE: ChatStoreState = {
  messages: [],
  connectionState: "disconnected",
  isTyping: false,
  isTurnActive: false,
  activeSessionId: getInitialActiveSessionId(),
  hasHydratedActiveSession: false,
  selectionBySession: {},
  questionsBySession: {},
}

export const chatAtom = atom<ChatStoreState>(DEFAULT_CHAT_STATE)
export const assistantDetailVisibilityAtom =
  atomWithStorage<AssistantDetailVisibility>(
    ASSISTANT_DETAIL_VISIBILITY_STORAGE_KEY,
    DEFAULT_ASSISTANT_DETAIL_VISIBILITY,
    assistantDetailVisibilityStorage,
    { getOnInit: true },
  )

const store = getDefaultStore()

export function getChatState() {
  return store.get(chatAtom)
}

export function updateChatStore(
  patch:
    | ChatStorePatch
    | ((prev: ChatStoreState) => ChatStorePatch | ChatStoreState),
) {
  store.set(chatAtom, (prev) => {
    const nextPatch = typeof patch === "function" ? patch(prev) : patch
    const next = { ...prev, ...nextPatch }

    if (next.activeSessionId !== prev.activeSessionId) {
      writeStoredSessionId(next.activeSessionId)
    }

    return next
  })
}

/** Adds a question, or replaces one re-sent with the same id. */
export function upsertQuestion(sessionId: string, question: PendingQuestion) {
  updateChatStore((prev) => {
    const list = prev.questionsBySession[sessionId] ?? []
    const index = list.findIndex((q) => q.questionId === question.questionId)
    const next =
      index >= 0
        ? list.map((q, i) =>
            i === index ? { ...question, requestId: q.requestId } : q,
          )
        : [...list, question]
    return {
      questionsBySession: { ...prev.questionsBySession, [sessionId]: next },
    }
  })
}

export function removeQuestion(sessionId: string, questionId: string) {
  updateChatStore((prev) => {
    const list = prev.questionsBySession[sessionId]
    if (!list) return {}
    const questionsBySession = { ...prev.questionsBySession }
    const next = list.filter((q) => q.questionId !== questionId)
    if (next.length > 0) questionsBySession[sessionId] = next
    else delete questionsBySession[sessionId]
    return { questionsBySession }
  })
}

export function patchQuestion(
  sessionId: string,
  questionId: string,
  patch: Partial<PendingQuestion>,
) {
  updateChatStore((prev) => {
    const list = prev.questionsBySession[sessionId]
    if (!list) return {}
    return {
      questionsBySession: {
        ...prev.questionsBySession,
        [sessionId]: list.map((q) =>
          q.questionId === questionId ? { ...q, ...patch } : q,
        ),
      },
    }
  })
}

/** Finds the session and question a request id was sent for. */
export function findQuestionByRequest(
  requestId: string,
): { sessionId: string; question: PendingQuestion } | undefined {
  for (const [sessionId, list] of Object.entries(
    getChatState().questionsBySession,
  )) {
    const question = list.find((q) => q.requestId === requestId)
    if (question) return { sessionId, question }
  }
  return undefined
}

export function clearFailedDraft() {
  updateChatStore({ failedDraft: undefined })
}

export { shouldShowAssistantMessage }
export type { AssistantDetailVisibility }
