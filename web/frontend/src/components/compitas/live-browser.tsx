import * as React from "react"
import { useTranslation } from "react-i18next"

import { liveSocketUrl } from "@/components/compitas/live"
import { Button } from "@/components/ui/button"

type Status = "connecting" | "up" | "down"

/**
 * The Compita's browser, live. Compa carries only the display's byte stream
 * from the machine; noVNC, bundled here, draws it, so nothing the machine
 * serves ever runs in the dashboard. It starts view-only: taking control is a
 * choice.
 */
export function LiveBrowser({ id }: { id: string }) {
  const { t } = useTranslation()
  const box = React.useRef<HTMLDivElement>(null)
  const client = React.useRef<{ viewOnly: boolean } | null>(null)
  const [control, setControl] = React.useState(false)
  const controlNow = React.useRef(false)
  const [status, setStatus] = React.useState<Status>("connecting")
  const [attempt, setAttempt] = React.useState(0)

  React.useEffect(() => {
    controlNow.current = control
    if (client.current) client.current.viewOnly = !control
  }, [control])

  React.useEffect(() => {
    let closed = false
    let rfb: { disconnect(): void } | undefined
    setStatus("connecting")
    // Loaded when a Compita's browser is first watched, not with the page.
    void import("@novnc/novnc/core/rfb").then(({ default: RFB }) => {
      if (closed || !box.current) return
      const c = new RFB(box.current, liveSocketUrl(id), {
        wsProtocols: ["binary"],
      })
      c.scaleViewport = true
      c.background = "#000"
      c.viewOnly = !controlNow.current
      c.addEventListener("connect", () => setStatus("up"))
      c.addEventListener("disconnect", () => setStatus("down"))
      // The display needs no password; if a machine asks for one, give up
      // rather than wait for a login nobody can type.
      c.addEventListener("credentialsrequired", () => c.disconnect())
      client.current = c
      rfb = c
    })
    return () => {
      closed = true
      client.current = null
      rfb?.disconnect()
    }
  }, [id, attempt])

  return (
    <div className="flex flex-col gap-1">
      <div
        ref={box}
        data-testid="live-browser"
        className="aspect-[16/10] w-full overflow-hidden rounded-md border bg-black"
      />
      <div className="flex items-center justify-between gap-2 text-xs">
        <label className="flex items-center gap-1">
          <input
            type="checkbox"
            checked={control}
            onChange={(e) => setControl(e.target.checked)}
          />
          {t("compitas.liveControl")}
        </label>
        {status === "connecting" && (
          <span className="text-muted-foreground">
            {t("compitas.liveConnecting")}
          </span>
        )}
        {status === "down" && (
          <span className="flex items-center gap-2">
            <span className="text-muted-foreground">
              {t("compitas.liveDown")}
            </span>
            <Button
              size="sm"
              variant="outline"
              onClick={() => setAttempt((n) => n + 1)}
            >
              {t("compitas.liveRetry")}
            </Button>
          </span>
        )}
      </div>
    </div>
  )
}
