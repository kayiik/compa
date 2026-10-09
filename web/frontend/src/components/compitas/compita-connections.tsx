import * as React from "react"
import { useTranslation } from "react-i18next"

import {
  type Connection,
  getConnections,
  removeConnection,
  setConnection,
} from "@/api/compute"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

/** Tools and integrations made for one Compita, as MCP servers. */
export function CompitaConnections({ id }: { id: string }) {
  const { t } = useTranslation()
  const [items, setItems] = React.useState<Connection[]>([])
  const [name, setName] = React.useState("")
  const [command, setCommand] = React.useState("")
  const [args, setArgs] = React.useState("")
  const [env, setEnv] = React.useState("")
  const [error, setError] = React.useState("")

  React.useEffect(() => {
    void getConnections(id).then(setItems, () => setItems([]))
  }, [id])

  const add = async () => {
    const vars: Record<string, string> = {}
    for (const line of env.split("\n")) {
      const at = line.indexOf("=")
      if (at > 0) vars[line.slice(0, at).trim()] = line.slice(at + 1).trim()
    }
    try {
      setItems(
        await setConnection(id, name.trim(), {
          command: command.trim(),
          args: args.trim() ? args.trim().split(/\s+/) : [],
          env: vars,
        }),
      )
      setName("")
      setCommand("")
      setArgs("")
      setEnv("")
      setError("")
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  return (
    <div className="flex flex-col gap-2 rounded-md border p-2 text-sm">
      <span className="font-medium">{t("compitas.connectionsTitle")}</span>
      <span className="text-muted-foreground">
        {t("compitas.connectionsHint")}
      </span>
      {items.map((c) => (
        <div key={c.name} className="flex items-center justify-between gap-2">
          <span className="truncate">
            <b>{c.name}</b> · {c.command} {c.args.join(" ")}
            {c.env_keys.length > 0 && ` · ${c.env_keys.join(", ")}`}
          </span>
          <Button
            size="sm"
            variant="outline"
            onClick={() =>
              void removeConnection(id, c.name).then(setItems, (e: unknown) =>
                setError(e instanceof Error ? e.message : String(e)),
              )
            }
          >
            {t("compitas.connectionRemove")}
          </Button>
        </div>
      ))}
      <div className="flex flex-wrap gap-2">
        <Input
          className="w-32"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder={t("compitas.connectionName")}
        />
        <Input
          className="w-48"
          value={command}
          onChange={(e) => setCommand(e.target.value)}
          placeholder={t("compitas.connectionCommand")}
        />
        <Input
          className="min-w-48 flex-1"
          value={args}
          onChange={(e) => setArgs(e.target.value)}
          placeholder={t("compitas.connectionArgs")}
        />
      </div>
      <textarea
        className="border-input bg-background min-h-16 rounded-md border p-2 text-xs"
        value={env}
        onChange={(e) => setEnv(e.target.value)}
        placeholder={t("compitas.connectionEnv")}
      />
      <div>
        <Button
          size="sm"
          disabled={!name.trim() || !command.trim()}
          onClick={() => void add()}
        >
          {t("compitas.connectionAdd")}
        </Button>
      </div>
      {error && <div className="text-destructive">{error}</div>}
    </div>
  )
}
