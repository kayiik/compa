import * as React from "react"

export interface LiveTool {
  tool: string
  state: string
  detail?: string
}

export interface LiveState {
  active: boolean
  text: string
  tools: LiveTool[]
  /** Where the current segment of text starts, for a provider that revises it. */
  segStart: number
}

export type LiveEvent =
  | { type: "snapshot"; active: boolean; text: string; tools: LiveTool[] }
  | { type: "begin" | "end" | "segment" | "reset" }
  | { type: "delta"; text: string }
  | { type: "tool"; tool: string; state: string; detail?: string }

export const emptyLive: LiveState = {
  active: false,
  text: "",
  tools: [],
  segStart: 0,
}

const maxTools = 30

/** The WebSocket that carries the live view of a Compita's browser through Compa. */
export function liveSocketUrl(id: string, loc: Location = window.location) {
  const scheme = loc.protocol === "https:" ? "wss" : "ws"
  return `${scheme}://${loc.host}/api/compitas/${encodeURIComponent(id)}/live/ws`
}

/** Folds one event of a Compita's turn into what the pane shows. It mirrors the host. */
export function applyLive(state: LiveState, ev: LiveEvent): LiveState {
  switch (ev.type) {
    case "snapshot":
      return {
        active: ev.active,
        text: ev.text,
        tools: ev.tools ?? [],
        segStart: ev.text.length,
      }
    case "begin":
      return { active: true, text: "", tools: [], segStart: 0 }
    case "end":
      return { ...state, active: false }
    case "segment": {
      const text = state.text ? state.text + "\n\n" : state.text
      return { ...state, text, segStart: text.length }
    }
    case "reset":
      return { ...state, text: state.text.slice(0, state.segStart) }
    case "delta":
      return { ...state, text: state.text + ev.text }
    case "tool": {
      if (ev.state === "start") {
        const tools = [
          ...state.tools,
          { tool: ev.tool, state: "start", detail: ev.detail },
        ]
        return { ...state, tools: tools.slice(-maxTools) }
      }
      const tools = [...state.tools]
      for (let i = tools.length - 1; i >= 0; i--) {
        if (tools[i].tool === ev.tool && tools[i].state === "start") {
          tools[i] = { ...tools[i], state: ev.state }
          break
        }
      }
      return { ...state, tools }
    }
    default:
      return state
  }
}

/**
 * Follows what a Compita is doing right now: the answer as it is written and
 * the tools it uses. onEnd runs when a turn finishes, so the caller can fetch
 * the final reply.
 */
export function useCompitaLive(id: string, onEnd: () => void): LiveState {
  const [state, setState] = React.useState<LiveState>(emptyLive)
  const onEndRef = React.useRef(onEnd)
  React.useEffect(() => {
    onEndRef.current = onEnd
  }, [onEnd])

  React.useEffect(() => {
    if (typeof EventSource === "undefined") return
    setState(emptyLive)
    const source = new EventSource(
      `/api/compitas/${encodeURIComponent(id)}/stream`,
    )
    source.onmessage = (m: MessageEvent<string>) => {
      let ev: LiveEvent
      try {
        ev = JSON.parse(m.data) as LiveEvent
      } catch {
        return
      }
      setState((s) => applyLive(s, ev))
      if (ev.type === "end") onEndRef.current()
    }
    return () => source.close()
  }, [id])

  return state
}
