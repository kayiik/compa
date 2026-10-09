# Changelog

Notable changes to Compa, newest first. Versions follow
[Semantic Versioning](https://semver.org/).

## Unreleased

First release.

### Added

- Compa is a personal AI assistant that runs on your computer: a web app for
  everyday use, and a Go runtime you can embed. A program that embeds Compa can
  give the agent its own identity with the `AGENT.md` keys `name`,
  `description`, `memory` and `privateWorkspace`, and require tool calls with
  `requireTools: true`. See [Embed the Go runtime](docs/embedding.md).
- Chat apps: the web chat, WhatsApp and Slack, plus the Slack and Teams
  webhooks for notifications. Slack needs the app token (`xapp-`) as well as the
  bot token.
- WhatsApp is a linked device of your account: link it with **Link WhatsApp**
  on the WhatsApp page or `compa-kernel auth whatsapp`. Photos, voice notes,
  audio, video and documents up to 50 MB reach the agent. See
  [WhatsApp](docs/use.md#whatsapp).
- Compa answers only its owner: the accounts in a channel's **Allow From**, in
  direct messages. Group chats, rooms and threads are ignored. While **Allow
  From** is empty, your first message makes a pairing request to approve. See
  [Who Compa answers](docs/use.md#who-compa-answers).
- Results of scheduled jobs, heartbeat messages and approval requests also go
  to every enabled Slack and Teams webhook. See
  [Webhooks and notifications](docs/use.md#webhooks-and-notifications).
- The `message` tool sends to any chat on a connected channel by default;
  `tools.message.targets: "current_chat"` keeps it to the current chat.
- Clear chat errors when a provider wants payment, the conversation is longer
  than the model's context window, or the model can't take tools or images.
- Google Gemini replies stream as they're written.
- `COMPA_CHANNELS_<NAME>_ENABLED` turns a `channel_list` entry on or off, and
  `COMPA_CHANNELS_WEB_STREAMING_ENABLED` the web chat's streaming. See
  [Environment variables](docs/cli.md#environment-variables).
- `--json` on `compa-kernel model`, `model auto-free`, `model ping`,
  `model roster`, `auth login`, `auth logout` and `auth status`. See
  [JSON output](docs/cli.md#json-output).
- [Gateway interface](docs/gateway.md) documents how programs talk to the
  gateway, as protocol 1. The web chat sends `turn.start` and `turn.end`
  frames, and serves its session history at `/web/sessions`.
- Each release archive holds `THIRD_PARTY_NOTICES`, the licenses of the
  third-party code in the programs. The installers keep it with `LICENSE` and
  `NOTICE`: beside the programs on Windows, in `~/.local/share/doc/compa` on
  macOS and Linux. Every build links go.mau.fi/libsignal, which is GPL-3.0; see
  [NOTICE](NOTICE).
- Built with Go 1.26.9 and golang.org/x/net 0.60.0, which fix vulnerabilities
  in HTTP/2, `net/http`, `crypto/tls` and `os`, on llmgw-core, llm-provider-auth
  and llm-translate 0.0.1.
