import { fireEvent, render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"

import "@/i18n"
import type { PendingQuestion } from "@/store/chat"

import { QuestionCard } from "./question-card"

const QUESTION: PendingQuestion = {
  questionId: "q1",
  expiresAt: Date.now() + 60_000,
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

function renderCard(question: PendingQuestion = QUESTION) {
  const onAnswer = vi.fn()
  const onCancel = vi.fn()
  render(
    <QuestionCard
      question={question}
      onAnswer={onAnswer}
      onCancel={onCancel}
    />,
  )
  return { onAnswer, onCancel }
}

describe("QuestionCard", () => {
  it("shows one question at a time, a tab each, its options numbered", () => {
    renderCard()
    expect(screen.getAllByRole("tab")).toHaveLength(2)
    expect(screen.getByRole("tab", { name: "Database" })).toHaveAttribute(
      "aria-selected",
      "true",
    )
    expect(screen.getByRole("radio", { name: /Postgres/ })).not.toBeChecked()
    expect(screen.getByText("Recommended")).toBeInTheDocument()
    expect(screen.getByText("Relational")).toBeInTheDocument()
    expect(screen.queryByRole("checkbox", { name: /Auth/ })).toBeNull()
  })

  it("moves on after a single choice and sends the answers in order", () => {
    const { onAnswer } = renderCard()
    fireEvent.click(screen.getByRole("radio", { name: /SQLite/ }))

    // The next question, which takes several choices.
    expect(screen.getByRole("tab", { name: "Extras" })).toHaveAttribute(
      "aria-selected",
      "true",
    )
    const submit = screen.getByRole("button", { name: "Submit" })
    expect(submit).toBeDisabled()
    fireEvent.click(screen.getByRole("checkbox", { name: /Auth/ }))
    fireEvent.click(screen.getByRole("checkbox", { name: /Cache/ }))
    expect(screen.getByRole("checkbox", { name: /Cache/ })).toBeChecked()
    fireEvent.change(
      screen.getByRole("textbox", { name: "Or type your own answer" }),
      { target: { value: " and logs " } },
    )
    expect(submit).toBeEnabled()

    fireEvent.click(submit)
    expect(onAnswer).toHaveBeenCalledWith("q1", [
      { selected: ["SQLite"], text: "" },
      { selected: ["Auth", "Cache"], text: "and logs" },
    ])
  })

  it("picks an option with its number key", () => {
    renderCard()
    const card = screen.getByRole("radio", { name: /Postgres/ })
    fireEvent.keyDown(card, { key: "2" })
    fireEvent.click(screen.getByRole("tab", { name: "Database" }))
    expect(screen.getByRole("radio", { name: /SQLite/ })).toBeChecked()
  })

  it("accepts text alone as an answer, sent with Enter", () => {
    const { onAnswer } = renderCard({
      ...QUESTION,
      questions: [QUESTION.questions[0]!],
    })
    expect(screen.queryByRole("tab")).toBeNull()
    const own = screen.getByRole("textbox", { name: "Or type your own answer" })
    fireEvent.change(own, { target: { value: "MySQL" } })
    fireEvent.keyDown(own, { key: "Enter" })
    expect(onAnswer).toHaveBeenCalledWith("q1", [
      { selected: [], text: "MySQL" },
    ])
  })

  it("cancels the question, with the button or Escape", () => {
    const { onCancel } = renderCard()
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }))
    fireEvent.keyDown(screen.getByRole("radio", { name: /Postgres/ }), {
      key: "Escape",
    })
    expect(onCancel).toHaveBeenCalledTimes(2)
    expect(onCancel).toHaveBeenCalledWith("q1")
  })

  it("shows a refused answer's error and lets the person try again", () => {
    renderCard({ ...QUESTION, requestId: "r1", error: "Pick one option" })
    expect(screen.getByRole("alert")).toHaveTextContent("Pick one option")
    expect(screen.getByRole("button", { name: "Cancel" })).toBeEnabled()
  })

  it("waits for question.resolved once sent and not refused", () => {
    renderCard({ ...QUESTION, requestId: "r1" })
    expect(screen.getByRole("radio", { name: /SQLite/ })).toBeDisabled()
    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled()
  })

  it("does not disable the card on the client clock", () => {
    renderCard({ ...QUESTION, expiresAt: Date.now() - 1 })
    expect(screen.getByRole("radio", { name: /SQLite/ })).toBeEnabled()
    expect(screen.getByRole("button", { name: "Cancel" })).toBeEnabled()
  })
})
