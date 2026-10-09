import * as React from "react"
import { useTranslation } from "react-i18next"

import {
  type ApprovalPreset,
  type ComputeAdded,
  type ComputeState,
  type Isolation,
  addCompita,
  addCompute,
  getComputeState,
  refreshCompute,
  refreshLocalCompute,
  reissueComputeToken,
  removeCompita,
  removeCompute,
  setComputeEnabled,
} from "@/api/compute"
import { CompitaChat } from "@/components/compitas/compita-chat"
import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

const selectClass =
  "border-input bg-background h-9 rounded-md border px-2 text-sm"

/**
 * Compitas: always-on peer agents, each bound to a compute.
 *
 * Compute is opt-in. A Compita's isolation is a choice made per Compita, and
 * only the levels the chosen compute can actually provide are offered.
 */
export function CompitasPage() {
  const { t } = useTranslation()
  const [state, setState] = React.useState<ComputeState | null>(null)
  const [error, setError] = React.useState("")
  const [chatWith, setChatWith] = React.useState("")
  const [issued, setIssued] = React.useState<Pick<
    ComputeAdded,
    "token" | "enroll_command" | "manual_command" | "expires_in_minutes"
  > | null>(null)
  const [remote, setRemote] = React.useState({
    name: "",
    mode: "dial_out" as "dial_out" | "dial_in",
    url: "",
    pin: "",
  })
  const [compita, setCompita] = React.useState({
    name: "",
    description: "",
    compute_id: "",
    isolation: "" as Isolation | "",
    approvals: "careful" as ApprovalPreset,
    browser: false,
  })

  const run = React.useCallback(async (job: () => Promise<ComputeState>) => {
    try {
      setState(await job())
      setError("")
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }, [])

  React.useEffect(() => {
    void run(getComputeState)
  }, [run])

  if (!state) {
    return (
      <div className="p-6">
        <PageHeader title={t("compitas.title")} />
        {error && <p className="text-destructive text-sm">{error}</p>}
      </div>
    )
  }

  const chosen = state.computes.find((c) => c.id === compita.compute_id)
  // A container brings its own browser; otherwise the compute needs one.
  const canBrowse =
    compita.isolation === "container" ||
    (!!compita.isolation && !!chosen?.capabilities.browser)
  const nameOf = (id: string) =>
    state.computes.find((c) => c.id === id)?.name ?? id

  return (
    <div className="flex flex-col gap-6 p-6">
      <PageHeader title={t("compitas.title")} />
      <p className="text-muted-foreground max-w-2xl text-sm">
        {t("compitas.intro")}
      </p>
      {error && <p className="text-destructive text-sm">{error}</p>}

      <label className="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          checked={state.enabled}
          onChange={(e) => void run(() => setComputeEnabled(e.target.checked))}
        />
        {t("compitas.enable")}
      </label>

      {state.enabled && (
        <>
          <section className="flex flex-col gap-3">
            <h2 className="font-medium">{t("compitas.computes")}</h2>
            {state.computes.map((c) => (
              <div
                key={c.id}
                className="flex items-center justify-between rounded-md border p-3 text-sm"
              >
                <div>
                  <div className="font-medium">{c.name}</div>
                  <div className="text-muted-foreground">
                    {c.kind === "local"
                      ? t("compitas.thisComputer")
                      : `${c.mode === "dial_out" ? t("compitas.dialOut") : t("compitas.dialIn")} · ${t(`compitas.status.${c.status}`)}`}
                    {" · "}
                    {c.kind === "remote" && c.status === "enrolled"
                      ? `${c.online ? t("compitas.online") : t("compitas.offline")} · `
                      : ""}
                    {c.capabilities.docker
                      ? `Docker ${c.capabilities.docker_version ?? ""}`
                      : t("compitas.noDocker")}
                    {" · "}
                    {t("compitas.compitaCount", { count: c.compita_count })}
                  </div>
                </div>
                <div className="flex gap-2">
                  {c.kind === "local" ? (
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => void run(refreshLocalCompute)}
                    >
                      {t("compitas.redetect")}
                    </Button>
                  ) : (
                    <>
                      {c.mode === "dial_in" && (
                        <Button
                          variant="outline"
                          size="sm"
                          onClick={() => void run(() => refreshCompute(c.id))}
                        >
                          {t("compitas.redetect")}
                        </Button>
                      )}
                      {c.status === "pending" && (
                        <Button
                          variant="outline"
                          size="sm"
                          onClick={async () => {
                            try {
                              setIssued(await reissueComputeToken(c.id))
                            } catch (e) {
                              setError(
                                e instanceof Error ? e.message : String(e),
                              )
                            }
                          }}
                        >
                          {t("compitas.newToken")}
                        </Button>
                      )}
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => void run(() => removeCompute(c.id))}
                      >
                        {t("compitas.remove")}
                      </Button>
                    </>
                  )}
                </div>
              </div>
            ))}

            <div className="flex flex-wrap items-end gap-2">
              <Input
                className="w-48"
                placeholder={t("compitas.computeName")}
                value={remote.name}
                onChange={(e) => setRemote({ ...remote, name: e.target.value })}
              />
              <select
                className={selectClass}
                value={remote.mode}
                onChange={(e) =>
                  setRemote({
                    ...remote,
                    mode: e.target.value as "dial_out" | "dial_in",
                  })
                }
              >
                <option value="dial_out">{t("compitas.dialOutHelp")}</option>
                <option value="dial_in">{t("compitas.dialInHelp")}</option>
              </select>
              {remote.mode === "dial_in" && (
                <Input
                  className="w-64"
                  placeholder="https://host:port"
                  value={remote.url}
                  onChange={(e) =>
                    setRemote({ ...remote, url: e.target.value })
                  }
                />
              )}
              {remote.mode === "dial_in" && (
                <Input
                  className="w-64"
                  placeholder={t("compitas.pinPlaceholder")}
                  value={remote.pin}
                  onChange={(e) =>
                    setRemote({ ...remote, pin: e.target.value })
                  }
                />
              )}
              <Button
                onClick={async () => {
                  try {
                    const added = await addCompute(remote)
                    setIssued(added)
                    setRemote({ ...remote, name: "", url: "", pin: "" })
                    setState(await getComputeState())
                    setError("")
                  } catch (e) {
                    setError(e instanceof Error ? e.message : String(e))
                  }
                }}
              >
                {t("compitas.addCompute")}
              </Button>
            </div>

            {issued && (
              <div className="rounded-md border p-3 text-sm">
                <p>
                  {t("compitas.runThis", {
                    minutes: issued.expires_in_minutes,
                  })}
                </p>
                <pre className="bg-muted mt-2 overflow-x-auto rounded p-2">
                  {issued.enroll_command}
                </pre>
                {issued.manual_command && (
                  <>
                    <p className="text-muted-foreground mt-2">
                      {t("compitas.runThisManual")}
                    </p>
                    <pre className="bg-muted mt-1 overflow-x-auto rounded p-2">
                      {issued.manual_command}
                    </pre>
                  </>
                )}
              </div>
            )}
          </section>

          <section className="flex flex-col gap-3">
            <h2 className="font-medium">{t("compitas.title")}</h2>
            {state.compitas.map((p) => (
              <div
                key={p.id}
                className="flex items-center justify-between rounded-md border p-3 text-sm"
              >
                <div>
                  <div className="font-medium">{p.name}</div>
                  <div className="text-muted-foreground">
                    {nameOf(p.compute_id)} · {p.isolation}
                    {p.description ? ` · ${p.description}` : ""}
                  </div>
                </div>
                <div className="flex gap-2">
                  <Button size="sm" onClick={() => setChatWith(p.id)}>
                    {t("compitas.chat")}
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => {
                      if (chatWith === p.id) setChatWith("")
                      void run(() => removeCompita(p.id))
                    }}
                  >
                    {t("compitas.remove")}
                  </Button>
                </div>
              </div>
            ))}

            <div className="flex flex-wrap items-end gap-2">
              <Input
                className="w-48"
                placeholder={t("compitas.compitaName")}
                value={compita.name}
                onChange={(e) =>
                  setCompita({ ...compita, name: e.target.value })
                }
              />
              <Input
                className="w-64"
                placeholder={t("compitas.description")}
                value={compita.description}
                onChange={(e) =>
                  setCompita({ ...compita, description: e.target.value })
                }
              />
              <select
                className={selectClass}
                value={compita.compute_id}
                onChange={(e) =>
                  setCompita({
                    ...compita,
                    compute_id: e.target.value,
                    isolation: "",
                  })
                }
              >
                <option value="">{t("compitas.pickCompute")}</option>
                {state.computes
                  .filter((c) => c.status === "enrolled")
                  .map((c) => (
                    <option key={c.id} value={c.id}>
                      {c.name}
                    </option>
                  ))}
              </select>
              <select
                className={selectClass}
                value={compita.isolation}
                disabled={!chosen}
                onChange={(e) =>
                  setCompita({
                    ...compita,
                    isolation: e.target.value as Isolation,
                  })
                }
              >
                <option value="">{t("compitas.pickIsolation")}</option>
                {chosen?.isolations.map((i) => (
                  <option key={i} value={i}>
                    {t(`compitas.isolation.${i}`)}
                  </option>
                ))}
              </select>
              <select
                className={selectClass}
                value={compita.approvals}
                onChange={(e) =>
                  setCompita({
                    ...compita,
                    approvals: e.target.value as ApprovalPreset,
                  })
                }
              >
                <option value="careful">
                  {t("compitas.approvalsCareful")}
                </option>
                <option value="open">{t("compitas.approvalsOpen")}</option>
              </select>
              {canBrowse && (
                <label className="flex items-center gap-1 text-sm">
                  <input
                    type="checkbox"
                    checked={compita.browser}
                    onChange={(e) =>
                      setCompita({ ...compita, browser: e.target.checked })
                    }
                  />
                  {t("compitas.browserLabel")}
                </label>
              )}
              <Button
                disabled={
                  !compita.name || !compita.compute_id || !compita.isolation
                }
                onClick={async () => {
                  await run(() =>
                    addCompita({
                      ...compita,
                      isolation: compita.isolation as Isolation,
                      browser: canBrowse && compita.browser,
                    }),
                  )
                  setCompita({ ...compita, name: "", description: "" })
                }}
              >
                {t("compitas.addCompita")}
              </Button>
            </div>
            {chatWith && state.compitas.some((p) => p.id === chatWith) && (
              <CompitaChat
                key={chatWith}
                id={chatWith}
                name={state.compitas.find((p) => p.id === chatWith)?.name ?? ""}
                browser={state.compitas.find((p) => p.id === chatWith)?.browser}
              />
            )}
          </section>
        </>
      )}
    </div>
  )
}
