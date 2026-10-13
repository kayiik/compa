import { IconCheck } from "@tabler/icons-react"
import { type KeyboardEvent, useEffect, useId, useRef, useState } from "react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import {
  type QuestionDraft,
  buildAnswers,
  isDraftComplete,
} from "@/features/chat/question-answers"
import { cn } from "@/lib/utils"
import type {
  ChatAnswer,
  ChatQuestionItem,
  PendingQuestion,
} from "@/store/chat"

const EMPTY_DRAFT: QuestionDraft = { selected: [], text: "" }

function isAnswered(draft: QuestionDraft) {
  return draft.selected.length > 0 || draft.text.trim() !== ""
}

interface QuestionPanelProps {
  item: ChatQuestionItem
  draft: QuestionDraft
  disabled: boolean
  onChange: (draft: QuestionDraft) => void
  onPicked: () => void
  onEnter: () => void
}

/** One question: its options as a numbered list, then a line of your own. */
function QuestionPanel({
  item,
  draft,
  disabled,
  onChange,
  onPicked,
  onEnter,
}: QuestionPanelProps) {
  const { t } = useTranslation()
  const id = useId()
  const listRef = useRef<HTMLDivElement>(null)

  const pick = (label: string) => {
    if (item.multiple) {
      const selected = draft.selected.includes(label)
        ? draft.selected.filter((value) => value !== label)
        : [...draft.selected, label]
      onChange({ ...draft, selected })
      return
    }
    // One choice replaces any typed answer, so it's clear which is sent.
    onChange({ selected: [label], text: "" })
    onPicked()
  }

  // Arrow keys move between the options, as in a list.
  const moveFocus = (event: KeyboardEvent<HTMLButtonElement>) => {
    if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return
    const options = Array.from(
      listRef.current?.querySelectorAll<HTMLElement>("[data-option]") ?? [],
    )
    const at = options.indexOf(event.currentTarget)
    const next = options[at + (event.key === "ArrowDown" ? 1 : -1)]
    if (next) {
      event.preventDefault()
      next.focus()
    }
  }

  return (
    <div className="flex flex-col gap-3">
      <p id={`${id}-question`} className="text-foreground text-sm leading-6">
        {item.question}
        {item.multiple && (
          <span className="text-muted-foreground">
            {" "}
            ({t("chat.question.chooseMany")})
          </span>
        )}
      </p>

      <div
        ref={listRef}
        role={item.multiple ? "group" : "radiogroup"}
        aria-labelledby={`${id}-question`}
        className="flex flex-col"
      >
        {item.options.map((option, index) => {
          const checked = draft.selected.includes(option.label)
          return (
            <button
              key={option.label}
              type="button"
              data-option
              role={item.multiple ? "checkbox" : "radio"}
              aria-checked={checked}
              disabled={disabled}
              onClick={() => pick(option.label)}
              onKeyDown={moveFocus}
              className={cn(
                "group focus-visible:ring-ring/50 flex w-full items-start gap-3 rounded-md px-2 py-1.5 text-left outline-none focus-visible:ring-[3px] disabled:cursor-not-allowed disabled:opacity-60",
                checked ? "bg-violet-500/12" : "hover:bg-muted/60",
              )}
            >
              <span
                aria-hidden
                className={cn(
                  "mt-0.5 flex size-5 shrink-0 items-center justify-center rounded text-xs tabular-nums",
                  checked
                    ? "bg-violet-500 text-white"
                    : "bg-muted text-muted-foreground",
                )}
              >
                {checked ? <IconCheck className="size-3.5" /> : index + 1}
              </span>
              <span className="flex min-w-0 flex-1 flex-col">
                <span className="text-foreground flex items-baseline gap-2 text-sm leading-6">
                  <span className="min-w-0">{option.label}</span>
                  {option.recommended && (
                    <span className="shrink-0 text-xs text-violet-400">
                      {t("chat.question.recommended")}
                    </span>
                  )}
                </span>
                {option.description && (
                  <span className="text-muted-foreground text-xs leading-5">
                    {option.description}
                  </span>
                )}
              </span>
            </button>
          )
        })}

        <div
          className={cn(
            "flex items-center gap-3 rounded-md px-2 py-1.5",
            draft.text.trim() !== "" && "bg-violet-500/12",
          )}
        >
          <span
            aria-hidden
            className="bg-muted text-muted-foreground flex size-5 shrink-0 items-center justify-center rounded text-xs tabular-nums"
          >
            {item.options.length + 1}
          </span>
          <input
            type="text"
            value={draft.text}
            disabled={disabled}
            aria-label={t("chat.question.other")}
            placeholder={t("chat.question.otherPlaceholder")}
            onChange={(event) =>
              onChange({
                selected: item.multiple ? draft.selected : [],
                text: event.target.value,
              })
            }
            onKeyDown={(event) => {
              if (event.key === "Enter" && !event.nativeEvent.isComposing) {
                event.preventDefault()
                onEnter()
              }
            }}
            className="placeholder:text-muted-foreground text-foreground min-w-0 flex-1 bg-transparent text-sm leading-6 outline-none disabled:cursor-not-allowed"
          />
        </div>
      </div>
    </div>
  )
}

interface QuestionCardProps {
  question: PendingQuestion
  onAnswer: (questionId: string, answers: ChatAnswer[]) => void
  onCancel: (questionId: string) => void
}

/**
 * The agent's questions, one at a time: a tab per question when there are
 * several, its options as a numbered list (press the number to pick), and a
 * line for an answer of your own.
 */
export function QuestionCard({
  question,
  onAnswer,
  onCancel,
}: QuestionCardProps) {
  const { t } = useTranslation()
  const titleId = useId()
  const cardRef = useRef<HTMLElement>(null)
  const items = question.questions
  const [drafts, setDrafts] = useState<QuestionDraft[]>(() =>
    items.map(() => ({ ...EMPTY_DRAFT })),
  )
  const [current, setCurrent] = useState(0)
  // Sent and not refused: wait for question.resolved. Expiry is the server's
  // to decide (question.resolved removes the card), not the client clock's.
  const disabled = Boolean(question.requestId) && !question.error
  const complete = isDraftComplete(drafts)
  const isLast = current === items.length - 1
  const item = items[current]!

  useEffect(() => {
    cardRef.current
      ?.querySelector<HTMLElement>("[data-option]:not(:disabled)")
      ?.focus({ preventScroll: true })
  }, [current])

  const setDraft = (index: number, draft: QuestionDraft) =>
    setDrafts((prev) => prev.map((value, i) => (i === index ? draft : value)))

  const submit = () => {
    if (!disabled && complete) {
      onAnswer(question.questionId, buildAnswers(drafts))
    }
  }

  // Next unanswered question, else submit when everything is answered.
  const advance = () => {
    if (!isLast) {
      setCurrent(current + 1)
    } else if (complete) {
      submit()
    }
  }

  // Number keys pick an option; the next number focuses the own-answer line.
  const onKeyDown = (event: KeyboardEvent<HTMLElement>) => {
    if (disabled || event.target instanceof HTMLInputElement) return
    if (event.key === "Escape") {
      onCancel(question.questionId)
      return
    }
    const n = Number(event.key)
    if (!Number.isInteger(n) || n < 1) return
    if (n <= item.options.length) {
      event.preventDefault()
      cardRef.current
        ?.querySelectorAll<HTMLElement>("[data-option]")
        [n - 1]?.click()
    } else if (n === item.options.length + 1) {
      event.preventDefault()
      cardRef.current?.querySelector<HTMLInputElement>("input")?.focus()
    }
  }

  return (
    <section
      ref={cardRef}
      aria-labelledby={titleId}
      onKeyDown={onKeyDown}
      className="bg-card border-border/60 flex w-full max-w-xl flex-col rounded-lg border"
    >
      <h2 id={titleId} className="sr-only">
        {t("chat.question.title", { count: items.length })}
      </h2>

      {items.length > 1 ? (
        <div
          role="tablist"
          aria-label={t("chat.question.title", { count: items.length })}
          className="border-border/60 flex gap-1 overflow-x-auto border-b px-2 pt-2"
        >
          {items.map((value, index) => {
            const answered = isAnswered(drafts[index] ?? EMPTY_DRAFT)
            return (
              <button
                key={index}
                type="button"
                role="tab"
                aria-selected={index === current}
                onClick={() => setCurrent(index)}
                className={cn(
                  "focus-visible:ring-ring/50 -mb-px flex max-w-48 shrink-0 items-center gap-1.5 rounded-t-md border-b-2 px-2.5 py-1.5 text-xs outline-none focus-visible:ring-[3px]",
                  index === current
                    ? "text-foreground border-violet-500"
                    : "text-muted-foreground hover:text-foreground border-transparent",
                )}
              >
                {answered && (
                  <IconCheck aria-hidden className="size-3 text-violet-400" />
                )}
                <span className="truncate">{value.header}</span>
              </button>
            )
          })}
        </div>
      ) : (
        <p className="text-muted-foreground px-4 pt-3 text-xs">{item.header}</p>
      )}

      <div
        role={items.length > 1 ? "tabpanel" : undefined}
        className="px-2 pt-3 pb-1 sm:px-3"
      >
        <div className="px-2">
          <QuestionPanel
            key={current}
            item={item}
            draft={drafts[current] ?? EMPTY_DRAFT}
            disabled={disabled}
            onChange={(draft) => setDraft(current, draft)}
            onPicked={() => {
              if (!isLast) setCurrent(current + 1)
            }}
            onEnter={advance}
          />
        </div>
      </div>

      {question.error && (
        <p role="alert" className="text-destructive px-4 pt-1 text-sm">
          {question.error}
        </p>
      )}

      <div className="flex items-center justify-end gap-2 px-3 pt-1 pb-3">
        <Button
          type="button"
          variant="ghost"
          size="sm"
          disabled={disabled}
          onClick={() => onCancel(question.questionId)}
        >
          {t("chat.question.cancel")}
        </Button>
        {isLast ? (
          <Button
            type="button"
            size="sm"
            disabled={disabled || !complete}
            onClick={submit}
          >
            {t("chat.question.submit")}
          </Button>
        ) : (
          <Button
            type="button"
            size="sm"
            disabled={disabled}
            onClick={() => setCurrent(current + 1)}
          >
            {t("chat.question.next")}
          </Button>
        )}
      </div>
    </section>
  )
}
