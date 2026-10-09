import { IconRobot, IconX } from "@tabler/icons-react"
import * as React from "react"
import { useTranslation } from "react-i18next"

import { getApprovals, getComputeState } from "@/api/compute"
import { CompitaChat } from "@/components/compitas/compita-chat"
import { Button } from "@/components/ui/button"

/** Side pane available on every page: one tab per Compita, each its own chat. */
export function CompitaDock() {
  const { t } = useTranslation()
  const [open, setOpen] = React.useState(false)
  const [active, setActive] = React.useState<string>()
  const [state, setState] = React.useState<Awaited<
    ReturnType<typeof getComputeState>
  > | null>(null)
  const [waiting, setWaiting] = React.useState(0)

  React.useEffect(() => {
    void getComputeState().then(setState, () => setState(null))
  }, [open])

  // Approvals owed to you show on the button even when the pane is closed.
  // While Compitas are off nothing is polled.
  const enabled = state?.enabled === true
  React.useEffect(() => {
    if (!enabled) return
    const load = () =>
      void getApprovals().then(
        (a) => setWaiting(a.length),
        () => setWaiting(0),
      )
    load()
    const timer = setInterval(load, 5000)
    return () => clearInterval(timer)
  }, [enabled])

  if (!state?.enabled || state.compitas.length === 0) return null

  const current =
    state.compitas.find((c) => c.id === active) ?? state.compitas[0]

  if (!open) {
    return (
      <Button
        className="fixed right-4 bottom-4 z-40 shadow-lg"
        variant={waiting > 0 ? "destructive" : "default"}
        onClick={() => setOpen(true)}
      >
        <IconRobot className="size-4" />
        {waiting > 0
          ? t("compitas.dockWaiting", { n: waiting })
          : t("compitas.dockOpen")}
      </Button>
    )
  }

  return (
    <aside className="bg-background fixed top-14 right-0 bottom-0 z-40 flex w-96 max-w-full flex-col gap-2 border-l p-3 shadow-lg">
      <div className="flex items-center justify-between">
        <div className="flex flex-wrap gap-1">
          {state.compitas.map((c) => (
            <Button
              key={c.id}
              size="sm"
              variant={c.id === current.id ? "default" : "outline"}
              onClick={() => setActive(c.id)}
            >
              {c.name}
            </Button>
          ))}
        </div>
        <Button
          size="icon"
          variant="ghost"
          aria-label={t("compitas.dockClose")}
          onClick={() => setOpen(false)}
        >
          <IconX className="size-4" />
        </Button>
      </div>
      <CompitaChat
        key={current.id}
        id={current.id}
        name={current.name}
        browser={current.browser}
      />
    </aside>
  )
}
