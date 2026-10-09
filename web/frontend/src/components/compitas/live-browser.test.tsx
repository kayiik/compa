import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { liveSocketUrl } from "@/components/compitas/live"
import "@/i18n"

import { LiveBrowser } from "./live-browser"

const made = vi.hoisted(() => ({
  clients: [] as Array<{
    url: string
    options: { wsProtocols?: string[] }
    viewOnly: boolean
    disconnect: ReturnType<typeof vi.fn>
    emit: (type: string) => void
  }>,
}))

vi.mock("@novnc/novnc/core/rfb", () => ({
  default: class FakeRFB extends EventTarget {
    viewOnly = false
    scaleViewport = false
    background = ""
    disconnect = vi.fn()
    url: string
    options: { wsProtocols?: string[] }
    constructor(
      _target: HTMLElement,
      url: string,
      options: { wsProtocols?: string[] },
    ) {
      super()
      this.url = url
      this.options = options
      made.clients.push(this as unknown as (typeof made.clients)[number])
    }
    emit(type: string) {
      this.dispatchEvent(new Event(type))
    }
  },
}))

describe("liveSocketUrl", () => {
  it("follows the page's own scheme and host, and escapes the id", () => {
    expect(
      liveSocketUrl("a b", {
        protocol: "https:",
        host: "compa.test",
      } as Location),
    ).toBe("wss://compa.test/api/compitas/a%20b/live/ws")
    expect(
      liveSocketUrl("ana", {
        protocol: "http:",
        host: "10.0.0.2:18800",
      } as Location),
    ).toBe("ws://10.0.0.2:18800/api/compitas/ana/live/ws")
  })
})

describe("LiveBrowser", () => {
  beforeEach(() => {
    made.clients.length = 0
  })

  it("connects through Compa, starts view-only, and lets the owner take control", async () => {
    render(<LiveBrowser id="ana" />)
    await waitFor(() => expect(made.clients).toHaveLength(1))
    const rfb = made.clients[0]
    expect(rfb.url).toMatch(/^wss?:\/\/.+\/api\/compitas\/ana\/live\/ws$/)
    expect(rfb.options.wsProtocols).toEqual(["binary"])
    expect(rfb.viewOnly).toBe(true)
    expect(screen.getByText("Connecting to the browser…")).toBeInTheDocument()

    rfb.emit("connect")
    await waitFor(() =>
      expect(screen.queryByText("Connecting to the browser…")).toBeNull(),
    )

    fireEvent.click(screen.getByRole("checkbox", { name: "Take control" }))
    expect(rfb.viewOnly).toBe(false)
    fireEvent.click(screen.getByRole("checkbox", { name: "Take control" }))
    expect(rfb.viewOnly).toBe(true)
  })

  it("says when the browser cannot be reached and reconnects on request", async () => {
    render(<LiveBrowser id="ana" />)
    await waitFor(() => expect(made.clients).toHaveLength(1))
    made.clients[0].emit("disconnect")
    await screen.findByText(/Can't reach the browser/)

    fireEvent.click(screen.getByRole("button", { name: "Reconnect" }))
    await waitFor(() => expect(made.clients).toHaveLength(2))
    expect(made.clients[0].disconnect).toHaveBeenCalled()
  })

  it("keeps the choice to control across a reconnect, and lets go on unmount", async () => {
    const { unmount } = render(<LiveBrowser id="ana" />)
    await waitFor(() => expect(made.clients).toHaveLength(1))
    fireEvent.click(screen.getByRole("checkbox", { name: "Take control" }))
    made.clients[0].emit("disconnect")
    fireEvent.click(await screen.findByRole("button", { name: "Reconnect" }))
    await waitFor(() => expect(made.clients).toHaveLength(2))
    expect(made.clients[1].viewOnly).toBe(false)

    unmount()
    expect(made.clients[1].disconnect).toHaveBeenCalled()
  })

  it("gives up when a machine asks for a password", async () => {
    render(<LiveBrowser id="ana" />)
    await waitFor(() => expect(made.clients).toHaveLength(1))
    made.clients[0].emit("credentialsrequired")
    expect(made.clients[0].disconnect).toHaveBeenCalled()
  })
})
