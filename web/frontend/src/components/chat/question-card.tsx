import { useEffect, useId, useState } from "react"
import { useTranslation } from "react-i18next"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Textarea } from "@/components/ui/textarea"
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

function useExpired(expiresAt: number) {
  const [expired, setExpired] = useState(
    () => expiresAt > 0 && Date.now() >= expiresAt,
  )
  useEffect(() => {
    if (expiresAt <= 0) return
    const remaining = expiresAt - Date.now()
    if (remaining <= 0) {
      setExpired(true)
      return
    }
    // setTimeout caps near 24.8 days; a longer wait never needs the timer.
    if (remaining > 2_000_000_000) return
    const timer = window.setTimeout(() => setExpired(true), remaining)
    return () => window.clearTimeout(timer)
  }, [expiresAt])
  return expired
}

interface QuestionSectionProps {
  item: ChatQuestionItem
  draft: QuestionDraft
  disabled: boolean
  onChange: (draft: QuestionDraft) => void
}

function QuestionSection({
  item,
  draft,
  disabled,
  onChange,
}: QuestionSectionProps) {
  const { t } = useTranslation()
  const id = useId()
  const otherId = `${id}-other`

  const toggle = (label: string, checked: boolean) => {
    if (item.multiple) {
      const selected = checked
        ? [...draft.selected, label]
        : draft.selected.filter((value) => value !== label)
      onChange({ ...draft, selected })
    } else if (checked) {
      onChange({ ...draft, selected: [label] })
    }
  }

  return (
    <fieldset className="flex min-w-0 flex-col gap-2" disabled={disabled}>
      <legend className="mb-2 flex flex-col gap-0.5">
        {item.header && (
          <span className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
            {item.header}
          </span>
        )}
        <span className="text-foreground text-sm font-medium">
          {item.question}
        </span>
        <span className="text-muted-foreground text-xs">
          {item.multiple
            ? t("chat.question.chooseMany")
            : t("chat.question.chooseOne")}
        </span>
      </legend>

      {item.options.map((option, index) => {
        const optionId = `${id}-${index}`
        const descriptionId = `${optionId}-description`
        const checked = draft.selected.includes(option.label)
        return (
          <label
            key={option.label}
            htmlFor={optionId}
            className={cn(
              "border-border/60 hover:bg-muted/50 has-focus-visible:ring-ring/50 flex cursor-pointer items-start gap-3 rounded-lg border px-3 py-2 has-focus-visible:ring-[3px]",
              checked && "border-violet-400/70 bg-violet-500/5",
              disabled && "cursor-not-allowed opacity-60",
            )}
          >
            <input
              id={optionId}
              type={item.multiple ? "checkbox" : "radio"}
              name={id}
              value={option.label}
              checked={checked}
              aria-describedby={option.description ? descriptionId : undefined}
              onChange={(event) => toggle(option.label, event.target.checked)}
              className="mt-0.5 size-4 shrink-0 accent-violet-500"
            />
            <span className="flex min-w-0 flex-col gap-0.5">
              <span className="flex flex-wrap items-center gap-2 text-sm">
                {option.label}
                {option.recommended && (
                  <Badge variant="secondary">
                    {t("chat.question.recommended")}
                  </Badge>
                )}
              </span>
              {option.description && (
                <span
                  id={descriptionId}
                  className="text-muted-foreground text-xs"
                >
                  {option.description}
                </span>
              )}
            </span>
          </label>
        )
      })}

      <label htmlFor={otherId} className="text-muted-foreground mt-1 text-xs">
        {t("chat.question.other")}
      </label>
      <Textarea
        id={otherId}
        value={draft.text}
        rows={2}
        placeholder={t("chat.question.otherPlaceholder")}
        onChange={(event) => onChange({ ...draft, text: event.target.value })}
        className="min-h-14 text-sm"
      />
    </fieldset>
  )
}

interface QuestionCardProps {
  question: PendingQuestion
  onAnswer: (questionId: string, answers: ChatAnswer[]) => void
  onCancel: (questionId: string) => void
}

export function QuestionCard({
  question,
  onAnswer,
  onCancel,
}: QuestionCardProps) {
  const { t } = useTranslation()
  const titleId = useId()
  const expired = useExpired(question.expiresAt)
  const [drafts, setDrafts] = useState<QuestionDraft[]>(() =>
    question.questions.map(() => ({ selected: [], text: "" })),
  )
  // Sent and not refused: wait for question.resolved.
  const pending = Boolean(question.requestId) && !question.error
  const disabled = expired || pending
  const complete = isDraftComplete(drafts)

  return (
    <section
      aria-labelledby={titleId}
      className="bg-card border-border/50 flex w-full max-w-xl flex-col gap-4 rounded-xl border px-4 py-4 sm:px-5"
    >
      <h2 id={titleId} className="text-foreground text-sm font-semibold">
        {t("chat.question.title", { count: question.questions.length })}
      </h2>

      {question.questions.map((item, index) => (
        <QuestionSection
          key={index}
          item={item}
          draft={drafts[index] ?? { selected: [], text: "" }}
          disabled={disabled}
          onChange={(draft) =>
            setDrafts((prev) =>
              prev.map((value, i) => (i === index ? draft : value)),
            )
          }
        />
      ))}

      {question.error && (
        <p role="alert" className="text-destructive text-sm">
          {question.error}
        </p>
      )}
      {expired && (
        <p role="status" className="text-muted-foreground text-sm">
          {t("chat.question.expired")}
        </p>
      )}

      <div className="flex justify-end gap-2">
        <Button
          type="button"
          variant="ghost"
          disabled={disabled}
          onClick={() => onCancel(question.questionId)}
        >
          {t("chat.question.cancel")}
        </Button>
        <Button
          type="button"
          disabled={disabled || !complete}
          onClick={() => onAnswer(question.questionId, buildAnswers(drafts))}
        >
          {t("chat.question.submit")}
        </Button>
      </div>
    </section>
  )
}
