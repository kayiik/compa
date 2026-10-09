import * as React from "react"
import { useTranslation } from "react-i18next"

import {
  type TriggersView,
  addSchedule,
  createTrigger,
  getTriggers,
  removeSchedule,
  revokeTrigger,
  toggleSchedule,
} from "@/api/compute"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

/** What starts a Compita's work without anyone chatting: schedules and a webhook. */
export function CompitaAutomation({ id }: { id: string }) {
  const { t } = useTranslation()
  const [view, setView] = React.useState<TriggersView | null>(null)
  const [goal, setGoal] = React.useState("")
  const [every, setEvery] = React.useState("60")
  const [instruction, setInstruction] = React.useState("")
  const [issued, setIssued] = React.useState<{
    token: string
    url: string
  } | null>(null)
  const [error, setError] = React.useState("")

  const load = React.useCallback(
    () => void getTriggers(id).then(setView, () => setView(null)),
    [id],
  )
  React.useEffect(() => {
    load()
    const timer = setInterval(load, 10000)
    return () => clearInterval(timer)
  }, [load])

  const run = async (job: () => Promise<unknown>) => {
    try {
      await job()
      setError("")
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
    load()
  }

  return (
    <div className="flex flex-col gap-2 rounded-md border p-2 text-sm">
      <span className="font-medium">{t("compitas.automationTitle")}</span>

      {view?.schedules.map((s) => (
        <div key={s.id} className="flex items-center justify-between gap-2">
          <span className="truncate" title={s.goal}>
            {t("compitas.scheduleEvery", { n: s.every_minutes })} · {s.goal}
          </span>
          <span className="flex shrink-0 gap-1">
            <Button
              size="sm"
              variant="outline"
              onClick={() =>
                void run(() => toggleSchedule(id, s.id, !s.enabled))
              }
            >
              {s.enabled
                ? t("compitas.scheduleDisable")
                : t("compitas.scheduleEnable")}
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={() => void run(() => removeSchedule(id, s.id))}
            >
              {t("compitas.scheduleRemove")}
            </Button>
          </span>
        </div>
      ))}
      <form
        className="flex gap-2"
        onSubmit={(e) => {
          e.preventDefault()
          const minutes = Number.parseInt(every, 10)
          if (!goal.trim() || !(minutes >= 1)) return
          void run(() => addSchedule(id, goal.trim(), minutes)).then(() =>
            setGoal(""),
          )
        }}
      >
        <Input
          value={goal}
          onChange={(e) => setGoal(e.target.value)}
          placeholder={t("compitas.scheduleGoalPlaceholder")}
        />
        <Input
          className="w-20"
          inputMode="numeric"
          value={every}
          onChange={(e) => setEvery(e.target.value)}
          aria-label={t("compitas.scheduleEveryLabel")}
        />
        <Button type="submit" disabled={!goal.trim()}>
          {t("compitas.scheduleAdd")}
        </Button>
      </form>

      <div className="flex flex-col gap-2 border-t pt-2">
        <span className="font-medium">{t("compitas.webhookTitle")}</span>
        {view?.trigger.enabled ? (
          <div className="flex items-center justify-between gap-2">
            <span className="text-muted-foreground truncate">
              {view.trigger.instruction} ·{" "}
              {t("compitas.webhookPending", { n: view.trigger.pending })}
            </span>
            <Button
              size="sm"
              variant="outline"
              onClick={() =>
                void run(async () => {
                  await revokeTrigger(id)
                  setIssued(null)
                })
              }
            >
              {t("compitas.webhookRevoke")}
            </Button>
          </div>
        ) : (
          <form
            className="flex gap-2"
            onSubmit={(e) => {
              e.preventDefault()
              if (!instruction.trim()) return
              void run(async () =>
                setIssued(await createTrigger(id, instruction.trim())),
              ).then(() => setInstruction(""))
            }}
          >
            <Input
              value={instruction}
              onChange={(e) => setInstruction(e.target.value)}
              placeholder={t("compitas.webhookInstruction")}
            />
            <Button type="submit" disabled={!instruction.trim()}>
              {t("compitas.webhookCreate")}
            </Button>
          </form>
        )}
        {issued && (
          <div className="flex flex-col gap-1 rounded-md border border-amber-500 p-2">
            <span>{t("compitas.webhookTokenOnce")}</span>
            <code className="text-xs break-all">{issued.url}</code>
            <code className="text-xs break-all">{issued.token}</code>
          </div>
        )}
      </div>
      {error && <div className="text-destructive">{error}</div>}
    </div>
  )
}
