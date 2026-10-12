import { beforeEach, describe, expect, it, vi } from "vitest"

import { getChatState, patchQuestion, updateChatStore } from "@/store/chat"

import { handleWebChatMessage, parseQuestionCreate } from "./protocol"

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), info: vi.fn(), success: vi.fn() },
}))

const SESSION = "s-1"

const CREATE = {
  question_id: "q1",
  expires_at: 1_900_000_000_000,
  questions: [
    {
      header: "Database",
      question: "Which database?",
      options: [
        { label: "Postgres", description: "Relational", recommended: true },
        { label: "SQLite", description: "Embedded" },
      ],
    },
    {
      header: "Extras",
      question: "Which extras?",
      multiple: true,
      options: [
        { label: "Auth", description: "Sign-in" },
        { label: "Cache", description: "Faster" },
      ],
    },
  ],
}

describe("question frames", () => {
  beforeEach(() => {
    vi.spyOn(console, "error").mockImplementation(() => {})
    updateChatStore({
      messages: [],
      isTyping: false,
      isTurnActive: false,
      questionsBySession: {},
    })
  })

  it("parses question.create", () => {
    expect(parseQuestionCreate(CREATE)).toEqual({
      questionId: "q1",
      expiresAt: 1_900_000_000_000,
      questions: [
        {
          header: "Database",
          question: "Which database?",
          options: [
            { label: "Postgres", description: "Relational", recommended: true },
            { label: "SQLite", description: "Embedded" },
          ],
        },
        {
          header: "Extras",
          question: "Which extras?",
          multiple: true,
          options: [
            { label: "Auth", description: "Sign-in" },
            { label: "Cache", description: "Faster" },
          ],
        },
      ],
    })
  })

  it("ignores a create without an id or questions", () => {
    expect(parseQuestionCreate({ questions: CREATE.questions })).toBeUndefined()
    expect(parseQuestionCreate({ question_id: "q1" })).toBeUndefined()
  })

  it("keeps one card when a reconnect sends the same question again", () => {
    handleWebChatMessage({ type: "question.create", payload: CREATE }, SESSION)
    handleWebChatMessage({ type: "question.create", payload: CREATE }, SESSION)
    expect(getChatState().questionsBySession[SESSION]).toHaveLength(1)
  })

  it("drops the card when the question is resolved", () => {
    handleWebChatMessage({ type: "question.create", payload: CREATE }, SESSION)
    handleWebChatMessage(
      {
        type: "question.resolved",
        payload: { question_id: "q1", outcome: "answered" },
      },
      SESSION,
    )
    expect(getChatState().questionsBySession[SESSION]).toBeUndefined()
  })

  it("puts a refused answer's error on its card and leaves the turn running", () => {
    handleWebChatMessage({ type: "question.create", payload: CREATE }, SESSION)
    patchQuestion(SESSION, "q1", { requestId: "req-1" })
    updateChatStore({ isTurnActive: true })
    handleWebChatMessage(
      {
        type: "error",
        payload: {
          request_id: "req-1",
          code: "invalid_answer",
          message: "Pick one option",
        },
      },
      SESSION,
    )
    expect(getChatState().questionsBySession[SESSION]?.[0]?.error).toBe(
      "Pick one option",
    )
    expect(getChatState().isTurnActive).toBe(true)
  })
})
