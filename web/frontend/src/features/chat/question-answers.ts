import type { ChatAnswer } from "@/store/chat"

export interface QuestionDraft {
  selected: string[]
  text: string
}

/** One answer per question, in order: the labels chosen and the text. */
export function buildAnswers(drafts: QuestionDraft[]): ChatAnswer[] {
  return drafts.map((draft) => ({
    selected: [...draft.selected],
    text: draft.text.trim(),
  }))
}

export function isDraftComplete(drafts: QuestionDraft[]) {
  return drafts.every(
    (draft) => draft.selected.length > 0 || draft.text.trim() !== "",
  )
}
