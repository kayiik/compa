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
  it("shows options, marks the recommended one, and uses checkboxes for multiple", () => {
    renderCard()
    expect(screen.getByRole("radio", { name: /Postgres/ })).not.toBeChecked()
    expect(screen.getByRole("radio", { name: /SQLite/ })).toBeInTheDocument()
    expect(screen.getByRole("checkbox", { name: /Auth/ })).toBeInTheDocument()
    expect(screen.getByRole("checkbox", { name: /Cache/ })).toBeInTheDocument()
    expect(screen.getAllByText("Recommended")).toHaveLength(1)
    expect(screen.getByText("Relational")).toBeInTheDocument()
  })

  it("enables Submit only when every question has an answer, and sends them in order", () => {
    const { onAnswer } = renderCard()
    const submit = screen.getByRole("button", { name: "Submit" })
    expect(submit).toBeDisabled()

    fireEvent.click(screen.getByRole("radio", { name: /SQLite/ }))
    expect(submit).toBeDisabled()

    fireEvent.click(screen.getByRole("checkbox", { name: /Auth/ }))
    fireEvent.click(screen.getByRole("checkbox", { name: /Cache/ }))
    const others = screen.getAllByRole("textbox", {
      name: "Or type your own answer",
    })
    fireEvent.change(others[1]!, { target: { value: " and logs " } })
    expect(submit).toBeEnabled()

    fireEvent.click(submit)
    expect(onAnswer).toHaveBeenCalledWith("q1", [
      { selected: ["SQLite"], text: "" },
      { selected: ["Auth", "Cache"], text: "and logs" },
    ])
  })

  it("accepts text alone as an answer", () => {
    const { onAnswer } = renderCard({
      ...QUESTION,
      questions: [QUESTION.questions[0]!],
    })
    fireEvent.change(
      screen.getByRole("textbox", { name: "Or type your own answer" }),
      { target: { value: "MySQL" } },
    )
    fireEvent.click(screen.getByRole("button", { name: "Submit" }))
    expect(onAnswer).toHaveBeenCalledWith("q1", [
      { selected: [], text: "MySQL" },
    ])
  })

  it("cancels the question", () => {
    const { onCancel } = renderCard()
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }))
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
