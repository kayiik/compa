import * as React from "react"
import { useTranslation } from "react-i18next"

import {
  type Approval,
  type ChatMessage,
  type ObjectiveView,
  decideApproval,
  getApprovals,
  getCompitaChat,
  getObjective,
  objectiveAction,
  sendCompitaMessage,
  startObjective,
} from "@/api/compute"
import { CompitaAutomation } from "@/components/compitas/compita-automation"
import { CompitaConnections } from "@/components/compitas/compita-connections"
import { useCompitaLive } from "@/components/compitas/live"
import { LiveBrowser } from "@/components/compitas/live-browser"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

/** Direct chat with one Compita. Each Compita keeps its own history. */
export function CompitaChat({
  id,
  name,
  browser,
}: {
  id: string
  name: string
  browser?: boolean
}) {
  const { t } = useTranslation()
  const [watching, setWatching] = React.useState(false)
  const [messages, setMessages] = React.useState<ChatMessage[]>([])
  const [draft, setDraft] = React.useState("")
  const [busy, setBusy] = React.useState(false)
  const [objective, setObjective] = React.useState<ObjectiveView | null>(null)
  const [approvals, setApprovals] = React.useState<Approval[]>([])
  const [goal, setGoal] = React.useState("")
  const [objectiveError, setObjectiveError] = React.useState("")
  const endRef = React.useRef<HTMLDivElement>(null)

  React.useEffect(() => {
    const load = () => {
      void getObjective(id).then(setObjective, () => setObjective(null))
      void getApprovals(id).then(setApprovals, () => setApprovals([]))
    }
    load()
    const timer = setInterval(load, 3000)
    return () => clearInterval(timer)
  }, [id])

  const answer = async (approvalId: string, approved: boolean) => {
    try {
      await decideApproval(approvalId, approved)
    } catch {
      // Already answered or gone; the next poll shows the truth.
    }
    setApprovals(await getApprovals(id).catch(() => []))
  }

  const act = async (run: () => Promise<ObjectiveView>) => {
    try {
      setObjective(await run())
      setObjectiveError("")
    } catch (e) {
      setObjectiveError(e instanceof Error ? e.message : String(e))
    }
  }
  const obj = objective?.objective
  const working = obj?.status === "active"

  React.useEffect(() => {
    void getCompitaChat(id).then(setMessages, () => setMessages([]))
  }, [id])

  // Pick up messages from colleagues while we're idle.
  React.useEffect(() => {
    if (busy) return
    const timer = setInterval(() => {
      void getCompitaChat(id).then(setMessages, () => {})
    }, 5000)
    return () => clearInterval(timer)
  }, [id, busy])

  // Follow the turn as it runs; when it ends the final reply is in the chat.
  const refreshChat = React.useCallback(() => {
    void getCompitaChat(id).then(setMessages, () => {})
  }, [id])
  const live = useCompitaLive(id, refreshChat)

  React.useEffect(() => {
    endRef.current?.scrollIntoView?.({ block: "end" })
  }, [messages, busy, live.text, live.tools.length])

  const send = async () => {
    const text = draft.trim()
    if (!text || busy) return
    setDraft("")
    setBusy(true)
    setMessages((m) => [
      ...m,
      { role: "user", text, at: new Date().toISOString() },
    ])
    try {
      const reply = await sendCompitaMessage(id, text)
      setMessages((m) => [...m, reply])
    } catch (e) {
      setMessages((m) => [
        ...m,
        {
          role: "compita",
          text: e instanceof Error ? e.message : String(e),
          error: true,
          at: new Date().toISOString(),
        },
      ])
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex flex-col gap-2 rounded-md border p-3">
      <h3 className="text-sm font-medium">
        {t("compitas.chatWith", { name })}
      </h3>
      <div className="flex flex-col gap-2 rounded-md border p-2 text-sm">
        <div className="flex items-center justify-between gap-2">
          <span className="font-medium">{t("compitas.objectiveTitle")}</span>
          {browser && (
            <button
              type="button"
              className="text-primary underline"
              onClick={() => setWatching((w) => !w)}
            >
              {watching ? t("compitas.liveHide") : t("compitas.watchLive")}
            </button>
          )}
          {obj && (
            <span className="text-muted-foreground">
              {t(
                `compitas.objectiveStatus${obj.status[0].toUpperCase()}${obj.status.slice(1)}`,
              )}
              {" · "}
              {t("compitas.objectiveProgress", {
                turns: obj.turns,
                max: obj.max_turns,
              })}
            </span>
          )}
        </div>
        {browser && watching && <LiveBrowser id={id} />}
        {obj && (
          <>
            <div className="whitespace-pre-wrap">{obj.goal}</div>
            {working && obj.claimed && (
              <div className="text-muted-foreground">
                {t("compitas.objectiveChecking")}
              </div>
            )}
            {(obj.summary || obj.reason || obj.note) && (
              <div className="text-muted-foreground whitespace-pre-wrap">
                {obj.summary || obj.reason || obj.note}
              </div>
            )}
          </>
        )}
        {approvals.map((a) => (
          <div
            key={a.id}
            className="flex flex-col gap-2 rounded-md border border-amber-500 p-2"
          >
            <div className="font-medium">{t("compitas.approvalWaiting")}</div>
            <pre className="max-h-40 overflow-auto text-xs whitespace-pre-wrap">
              {a.summary}
            </pre>
            <div className="flex gap-2">
              <Button size="sm" onClick={() => void answer(a.id, true)}>
                {t("compitas.approve")}
              </Button>
              <Button
                size="sm"
                variant="outline"
                onClick={() => void answer(a.id, false)}
              >
                {t("compitas.deny")}
              </Button>
            </div>
          </div>
        ))}
        {obj &&
          (working ||
            obj.status === "paused" ||
            obj.status === "blocked" ||
            obj.status === "exhausted") && (
            <div className="flex gap-2">
              {working ? (
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => void act(() => objectiveAction(id, "pause"))}
                >
                  {t("compitas.objectivePause")}
                </Button>
              ) : (
                <Button
                  size="sm"
                  onClick={() => void act(() => objectiveAction(id, "resume"))}
                >
                  {t("compitas.objectiveResume")}
                </Button>
              )}
              <Button
                size="sm"
                variant="outline"
                onClick={() => void act(() => objectiveAction(id, "stop"))}
              >
                {t("compitas.objectiveStop")}
              </Button>
            </div>
          )}
        {(!obj || ["done", "stopped"].includes(obj.status)) && (
          <form
            className="flex gap-2"
            onSubmit={(e) => {
              e.preventDefault()
              if (!goal.trim()) return
              void act(() => startObjective(id, goal.trim())).then(() =>
                setGoal(""),
              )
            }}
          >
            <Input
              value={goal}
              onChange={(e) => setGoal(e.target.value)}
              placeholder={t("compitas.objectivePlaceholder")}
            />
            <Button type="submit" disabled={!goal.trim()}>
              {t("compitas.objectiveStart")}
            </Button>
          </form>
        )}
        {objectiveError && (
          <div className="text-destructive">{objectiveError}</div>
        )}
      </div>
      <CompitaAutomation id={id} />
      <CompitaConnections id={id} />
      <div className="flex max-h-96 min-h-32 flex-col gap-2 overflow-y-auto text-sm">
        {messages.map((m, i) => (
          <div
            key={i}
            className={
              m.role === "user"
                ? "bg-muted self-end rounded-md px-3 py-2"
                : m.role === "peer" ||
                    m.role === "objective" ||
                    m.role === "approval"
                  ? "text-muted-foreground self-start rounded-md border border-dashed px-3 py-2"
                  : m.error
                    ? "text-destructive self-start whitespace-pre-wrap"
                    : "self-start whitespace-pre-wrap"
            }
          >
            {m.text}
          </div>
        ))}
        {live.active && (live.text || live.tools.length > 0) && (
          <div className="flex flex-col gap-1 self-start">
            {live.tools.map((tool, i) => (
              <div key={i} className="text-muted-foreground text-xs">
                {tool.state === "start"
                  ? "▶"
                  : tool.state === "error"
                    ? "✗"
                    : "✓"}{" "}
                {tool.tool}
                {tool.detail ? `: ${tool.detail}` : ""}
              </div>
            ))}
            {live.text && (
              <div className="whitespace-pre-wrap">
                {live.text}
                <span className="animate-pulse">▍</span>
              </div>
            )}
          </div>
        )}
        {busy && !(live.active && (live.text || live.tools.length > 0)) && (
          <div className="text-muted-foreground">{t("compitas.thinking")}</div>
        )}
        <div ref={endRef} />
      </div>
      <form
        className="flex gap-2"
        onSubmit={(e) => {
          e.preventDefault()
          void send()
        }}
      >
        <Input
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          placeholder={t("compitas.messagePlaceholder")}
        />
        <Button type="submit" disabled={busy || !draft.trim()}>
          {t("compitas.send")}
        </Button>
      </form>
    </div>
  )
}
