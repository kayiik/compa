// noVNC ships no types for its ES modules; this is the part Compa uses.
declare module "@novnc/novnc/core/rfb" {
  export interface RFBOptions {
    shared?: boolean
    credentials?: { username?: string; password?: string; target?: string }
    wsProtocols?: string[]
  }

  export default class RFB extends EventTarget {
    constructor(target: HTMLElement, url: string, options?: RFBOptions)
    viewOnly: boolean
    scaleViewport: boolean
    resizeSession: boolean
    background: string
    focusOnClick: boolean
    disconnect(): void
  }
}
