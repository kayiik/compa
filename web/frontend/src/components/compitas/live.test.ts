import { describe, expect, it } from "vitest"

import { type LiveState, applyLive, emptyLive } from "./live"

const run = (events: Parameters<typeof applyLive>[1][], from = emptyLive) =>
  events.reduce<LiveState>((s, e) => applyLive(s, e), from)

describe("applyLive", () => {
  it("builds the answer from segments and deltas, and starts clean each turn", () => {
    const s = run([
      { type: "begin" },
      { type: "segment" },
      { type: "delta", text: "I will look. " },
      { type: "segment" },
      { type: "delta", text: "Draft" },
      { type: "reset" },
      { type: "delta", text: "Final answer" },
    ])
    expect(s.active).toBe(true)
    expect(s.text).toBe("I will look. \n\nFinal answer")
    expect(run([{ type: "begin" }], s)).toEqual({
      active: true,
      text: "",
      tools: [],
      segStart: 0,
    })
  })

  it("tracks tools from start to end or error, and ends the turn", () => {
    const s = run([
      { type: "begin" },
      { type: "tool", tool: "exec", state: "start", detail: "ls" },
      { type: "tool", tool: "web", state: "start" },
      { type: "tool", tool: "exec", state: "end" },
      { type: "tool", tool: "web", state: "error" },
      { type: "end" },
    ])
    expect(s.active).toBe(false)
    expect(s.tools).toEqual([
      { tool: "exec", state: "end", detail: "ls" },
      { tool: "web", state: "error", detail: undefined },
    ])
  })

  it("takes a snapshot as the whole truth and keeps only recent tools", () => {
    const s = run([
      {
        type: "snapshot",
        active: true,
        text: "so far",
        tools: [{ tool: "exec", state: "start" }],
      },
      { type: "delta", text: " and more" },
    ])
    expect(s).toMatchObject({ active: true, text: "so far and more" })
    const many = run(
      Array.from({ length: 40 }, (_, i) => ({
        type: "tool" as const,
        tool: `t${i}`,
        state: "start",
      })),
    )
    expect(many.tools).toHaveLength(30)
    expect(many.tools[0].tool).toBe("t10")
  })
})
