import { IconCheck } from "@tabler/icons-react"
import {
  type KeyboardEvent,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
} from "react"
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

const OPTION_SELECTOR = "input[data-option]:not(:disabled)"

const ARROW_KEYS = new Set(["ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight"])

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
  // Arrow keys move a native radio's selection; that browses, it doesn't pick.
  const arrowing = useRef(false)

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
    if (!arrowing.current) onPicked()
  }

  const ownId = `${id}-own`
  const ownCount = item.options.length + 1

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

      <div className="flex flex-col">
        <div
          role={item.multiple ? "group" : "radiogroup"}
          aria-labelledby={`${id}-question`}
          className="flex flex-col"
        >
          {item.options.map((option, index) => {
            const checked = draft.selected.includes(option.label)
            const labelId = `${id}-option-${index}`
            const descriptionId = `${id}-description-${index}`
            return (
              <label
                key={option.label}
                className={cn(
                  "has-[input:focus-visible]:ring-ring/50 flex w-full cursor-pointer items-start gap-3 rounded-md px-2 py-1.5 text-left has-[input:disabled]:cursor-not-allowed has-[input:disabled]:opacity-60 has-[input:focus-visible]:ring-[3px]",
                  checked ? "bg-violet-500/12" : "hover:bg-muted/60",
                )}
              >
                <input
                  type={item.multiple ? "checkbox" : "radio"}
                  name={`${id}-choice`}
                  value={option.label}
                  data-option
                  checked={checked}
                  disabled={disabled}
                  aria-labelledby={labelId}
                  aria-describedby={
                    option.description ? descriptionId : undefined
                  }
                  onChange={() => pick(option.label)}
                  onKeyDown={(event) => {
                    if (ARROW_KEYS.has(event.key)) arrowing.current = true
                  }}
                  onKeyUp={() => {
                    arrowing.current = false
                  }}
                  className="peer sr-only"
                />
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
                  <span
                    id={labelId}
                    className="text-foreground flex items-baseline gap-2 text-sm leading-6"
                  >
                    <span className="min-w-0">{option.label}</span>
                    {option.recommended && (
                      <span className="shrink-0 text-xs text-violet-400">
                        {t("chat.question.recommended")}
                      </span>
                    )}
                  </span>
                  {option.description && (
                    <span
                      id={descriptionId}
                      className="text-muted-foreground text-xs leading-5"
                    >
                      {option.description}
                    </span>
                  )}
                </span>
              </label>
            )
          })}
        </div>

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
            {ownCount}
          </span>
          <label htmlFor={ownId} className="sr-only">
            {t("chat.question.other")}
          </label>
          <input
            id={ownId}
            type="text"
            data-own
            value={draft.text}
            disabled={disabled}
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
  const baseId = useId()
  const titleId = `${baseId}-title`
  const panelId = `${baseId}-panel`
  const tabId = (index: number) => `${baseId}-tab-${index}`
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
  const tabbed = items.length > 1

  const focusFirstOption = () =>
    cardRef.current
      ?.querySelector<HTMLElement>(OPTION_SELECTOR)
      ?.focus({ preventScroll: true })

  // Follow a question change with focus only when focus is already in the
  // card: never steal it from the composer (nor on mount).
  useEffect(() => {
    const card = cardRef.current
    const active = document.activeElement
    if (!card || !active || !card.contains(active)) return
    if (active.getAttribute("role") === "tab") return
    focusFirstOption()
  }, [current])

  // While sending keep focus on the card, not body; after a refusal put it
  // back on the current question if it was lost.
  const wasDisabled = useRef(disabled)
  useLayoutEffect(() => {
    const card = cardRef.current
    const active = document.activeElement
    if (card && disabled && !wasDisabled.current) {
      if (active && card.contains(active) && active !== card) {
        card.focus({ preventScroll: true })
      }
    } else if (card && !disabled && wasDisabled.current) {
      if (!active || active === document.body || active === card) {
        focusFirstOption()
      }
    }
    wasDisabled.current = disabled
  }, [disabled])

  const setDraft = (index: number, draft: QuestionDraft) =>
    setDrafts((prev) => prev.map((value, i) => (i === index ? draft : value)))

  const submit = () => {
    if (!disabled && complete) {
      // Before the controls disable, so focus doesn't fall to body.
      cardRef.current?.focus({ preventScroll: true })
      onAnswer(question.questionId, buildAnswers(drafts))
    }
  }

  // Only past an answered question: the next one, else submit when all are
  // answered, else the first one still unanswered.
  const advance = () => {
    if (!isAnswered(drafts[current] ?? EMPTY_DRAFT)) return
    if (!isLast) {
      setCurrent(current + 1)
    } else if (complete) {
      submit()
    } else {
      const first = drafts.findIndex((draft) => !isAnswered(draft))
      if (first >= 0) setCurrent(first)
    }
  }

  const onTabKeyDown = (event: KeyboardEvent<HTMLButtonElement>) => {
    let next: number
    if (event.key === "ArrowRight") next = (current + 1) % items.length
    else if (event.key === "ArrowLeft")
      next = (current - 1 + items.length) % items.length
    else if (event.key === "Home") next = 0
    else if (event.key === "End") next = items.length - 1
    else return
    event.preventDefault()
    setCurrent(next)
    document.getElementById(tabId(next))?.focus()
  }

  // Escape cancels anywhere in the card; number keys pick an option, the
  // next number focuses the own-answer line.
  const onKeyDown = (event: KeyboardEvent<HTMLElement>) => {
    if (disabled || event.nativeEvent.isComposing) return
    if (event.key === "Escape") {
      event.preventDefault()
      onCancel(question.questionId)
      return
    }
    if (
      event.target instanceof HTMLInputElement &&
      event.target.type === "text"
    )
      return
    if (
      event.ctrlKey ||
      event.metaKey ||
      event.altKey ||
      event.shiftKey ||
      event.repeat
    )
      return
    if (!/^[1-9]$/.test(event.key)) return
    const n = Number(event.key)
    if (n <= item.options.length) {
      event.preventDefault()
      cardRef.current
        ?.querySelectorAll<HTMLInputElement>("input[data-option]")
        [n - 1]?.click()
    } else if (n === item.options.length + 1) {
      event.preventDefault()
      cardRef.current
        ?.querySelector<HTMLInputElement>("input[data-own]")
        ?.focus()
    }
  }

  return (
    <section
      ref={cardRef}
      tabIndex={-1}
      aria-labelledby={titleId}
      onKeyDown={onKeyDown}
      className="bg-card border-border/60 flex w-full max-w-xl flex-col rounded-lg border outline-none"
    >
      <h2 id={titleId} className="sr-only">
        {t("chat.question.title", { count: items.length })}
      </h2>

      {tabbed ? (
        <div
          role="tablist"
          aria-label={t("chat.question.tabs")}
          className="border-border/60 flex gap-1 overflow-x-auto border-b px-2 pt-2"
        >
          {items.map((value, index) => {
            const answered = isAnswered(drafts[index] ?? EMPTY_DRAFT)
            const selected = index === current
            return (
              <button
                key={index}
                id={tabId(index)}
                type="button"
                role="tab"
                aria-selected={selected}
                aria-controls={panelId}
                tabIndex={selected ? 0 : -1}
                onClick={() => setCurrent(index)}
                onKeyDown={onTabKeyDown}
                className={cn(
                  "focus-visible:ring-ring/50 -mb-px flex max-w-48 shrink-0 items-center gap-1.5 rounded-t-md border-b-2 px-2.5 py-1.5 text-xs outline-none focus-visible:ring-[3px]",
                  selected
                    ? "text-foreground border-violet-500"
                    : "text-muted-foreground hover:text-foreground border-transparent",
                )}
              >
                {answered && (
                  <IconCheck aria-hidden className="size-3 text-violet-400" />
                )}
                <span className="truncate">{value.header}</span>
                {answered && (
                  <span className="sr-only">
                    {" "}
                    ({t("chat.question.answered")})
                  </span>
                )}
              </button>
            )
          })}
        </div>
      ) : (
        <p className="text-muted-foreground px-4 pt-3 text-xs">{item.header}</p>
      )}

      <div
        id={tabbed ? panelId : undefined}
        role={tabbed ? "tabpanel" : undefined}
        aria-labelledby={tabbed ? tabId(current) : undefined}
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

      {isLast && !complete && !disabled && (
        <p className="text-muted-foreground px-4 pb-3 text-right text-xs">
          {t("chat.question.incomplete")}
        </p>
      )}
    </section>
  )
}
