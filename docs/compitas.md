# Compitas

A Compita is an agent that keeps running beside Compa, like a colleague. It has
a name, a workspace and a chat of its own, and you can give it a goal to work
on until it is done. Each Compita runs on a **compute**: this computer, or
another machine you add. Compitas can message one another. They are off until
you turn them on.

## Turn them on

1. Open **Compitas**, under **Agent**, and tick **Enable Compitas support**.
   **This computer** appears as a compute.
2. Press **Create Compita**. Choose a name, a compute, an
   [isolation level](#isolation) and whether it asks you before risky actions;
   it can have a [browser](#browser).
3. Open its chat from the list, or from the **Compitas** button that appears on
   every page once you have one. Under **Objective**, write a goal and press
   **Start objective**.

A Compita keeps its files and settings in its own folder, `compitas/<id>` in
Compa's home on its compute. It copies the model setup of that machine (its
config, model catalogs, and the keys of its providers and tools), but not the
machine's chat apps, hooks or MCP servers, nor their secrets. What Compa itself
keeps about a Compita on the Compa host (its chat, objective and triggers) is in
`compita-state/<id>`, outside the folder a container mounts.

## Computes

A compute is a machine that Compitas run on. **This computer** is always there;
add others under **Add compute**.

| Mode | Who connects | Use it when |
|---|---|---|
| Dial out | The machine connects to Compa. | The machine is behind a firewall or NAT. The default. |
| Dial in | Compa connects to the machine. | The machine has an address Compa can reach. |

**Dial out.** Compa shows a command like this:

```sh
curl -fsSL 'https://compa.example/api/compute/install.sh?token=…' | sh
```

Run it on the other machine within 15 minutes. The script fetches this Compa's
`compa-kernel`, checks it against a SHA-256 the script carries, enrolls the
machine with the one-time token, connects the
[free providers](use.md#models) if the machine has no model setup, and keeps
`compa-kernel compute serve` running: as a systemd user service where there is
systemd, otherwise you keep it running. The kernel comes from this Compa, so
only a machine with the same operating system and processor can use the line.
On any other machine, install `compa-kernel` and run the commands Compa shows
below the line:

```sh
compa-kernel compute enroll --url <address of Compa> --token <token>
compa-kernel compute serve
```

Use HTTPS for a Compa that isn't on your own network: the script, the kernel
and the token travel over the connection. See
[LAN access](install.md#lan-access).

**Dial in.** On the machine, run
`compa-kernel compute serve --listen :8787 --secret <secret>`, then add it in
Compa with its address. `--tls` serves HTTPS with a self-signed certificate and
prints its fingerprint; enter the fingerprint when you add the compute, and
Compa trusts only that certificate.

Each machine uses its own models, so connect a provider on every machine; the
install line does that for you with the free ones.
[`compa-kernel compute`](cli.md#compute) lists the commands.

## Isolation

You choose how a Compita is kept apart from the others on its compute when you
create it.

| Level | What it means |
|---|---|
| Whole machine | The Compita has the compute to itself. Not offered for this computer, which Compa shares. |
| Docker container | Each turn runs in its own container, with only the Compita's folder mounted. Needs Docker on the compute. |
| Separate profile, Shared with others | Turns run on the machine, as your user, in the Compita's own folder (and its own browser profile). Nothing stops one Compita from reaching the machine's files or another Compita's folder. |

Container turns run as your user, with limits on memory (2 GB; 3 GB with a
browser), processors (2) and processes, all Linux capabilities dropped and
`no-new-privileges`. Build the images once from a checkout of this repository:

```sh
make compita-images
```

That builds `compa-compita:local` and, for Compitas with a browser,
`compa-compita-browser:local`, from `docker/`.

A compute runs the turns of different Compitas side by side, and one turn at a
time for each Compita. Compitas on one compute share its processor, memory and
provider keys: a container keeps their files apart, not their trust.

## Objectives

An objective is a goal a Compita works on over as many turns as it takes. It
ends in one of these states:

| State | What happened |
|---|---|
| Done | The Compita says it reached the goal, and a separate check turn confirms it against real files and command output. If the check fails, it goes back to work with what is missing. |
| Blocked | It says exactly what it needs and waits for you. **Resume** continues it. |
| Out of budget | 50 turns or 120 minutes, whichever comes first. **Resume** adds the same again. |
| Paused, Stopped | You did that. |

It also stops as blocked when it gives the same reply four times in a row or
fails eight turns in a row. A compute that is offline is waited for, not
counted as a failure. Compa keeps an objective across restarts: an active one
continues where it was. The Compita writes its acceptance criteria and progress
to a `PROGRESS-<time>.md` file in its workspace.

## Automation

Work can start while nobody is chatting.

- **Schedule:** "every N minutes, work on this goal". Each run is an objective
  that starts when the Compita is free.
- **Webhook:** give it an instruction, such as "triage the issue". Compa shows
  an address and a token, once. Send events to
  `POST /api/compitas/<id>/trigger` with the token as a bearer token
  (`Authorization: Bearer <token>`), or point a GitHub webhook at it with the
  token as the secret; Compa checks the `X-Hub-Signature-256` signature. Events
  that arrive while the Compita is busy wait in a queue of 20. The Compita is
  told they are untrusted data, not instructions.

A Compita that is blocked or paused gets no new work until you resume it.

## Approvals

A Compita you create with **Ask me before risky actions** (the default) asks
you before a tool that writes outside your computer, is destructive or may cost
money, and before running a command directly on a machine rather than in a
container. Everything else follows the machine's own
[approval policy](use.md#approvals). **Follow this machine's policy** leaves
the policy as it is.

When it asks, its turn waits where it is. The request appears in the Compita's
panel with **Approve** and **Deny**, and the **Compitas** button shows how many
are waiting. Your answer continues the same turn; no answer within 10 minutes
refuses it. Each request and answer stays in the Compita's chat.

## Chat

Each Compita has its own chat. Its reply appears as it is written: first the
tools it uses, then the answer. Anyone with the Compita open sees the same, and
someone who opens it in the middle of a turn sees what was said so far. The
finished reply is also saved in the chat, so closing the page loses nothing. If
the model's provider can't stream, or fails to, the answer arrives at once; the
tools still show as they run.

## Browser

A Compita can have a browser: Chromium, driven through the
[Playwright MCP](https://github.com/microsoft/playwright-mcp) server, which the
agent sees as `mcp_browser_…` tools. Its profile is in the Compita's own
folder, so logins last between turns.

- **In a container:** each turn runs in the browser image, which has Chromium
  and a virtual screen.
- **On the machine:** the compute must have a browser. A compute reports one
  when `playwright-mcp` is in its `PATH`, as in the browser image, so a compute
  you run from that image gives its Compitas a browser at every isolation
  level:

  ```sh
  docker run -d --name compa-compute --shm-size=1g -v compa-data:/data \
    compa-compita-browser:local compute serve
  ```

  Enroll it first with `compute enroll`, in the same volume. Outside the
  image, Chromium keeps its sandbox unless the machine sets
  `COMPA_BROWSER_NO_SANDBOX=1`, and it needs a display (`DISPLAY`).

**Watch the browser live** in the Compita's panel shows its screen, on any
compute, from any device that can open Compa. It is view-only until you tick
**Take control**, which sends your mouse and keyboard to the browser: to sign
in, to solve a captcha, or to steer.

- On this computer, Compa connects to the screen directly. For a container,
  that is the loopback port Docker publishes while a turn runs.
- On a dial-in compute, Compa opens a WebSocket to the machine's
  `/live/tunnel` with the dial secret, and the pinned certificate if there is
  one.
- On a dial-out compute, Compa asks the machine on its next poll, and the
  machine connects back to Compa with its credential, so it still needs no
  open port.
- A container's browser exists only while a turn runs. Between turns the panel
  says it can't reach the browser; press **Reconnect**. A compute run from the
  browser image keeps its browser up.
- Only the screen's data crosses. Compa draws it with a copy of
  [noVNC](https://novnc.com) built into its web UI, and never serves a page
  from the machine, so a machine can't run script in your dashboard. The
  connection needs your login and refuses pages from other sites. Over plain
  HTTP on an address other than `localhost`, the browser warns that noVNC needs
  a secure context: use HTTPS beyond your own network.

## Connections

A connection is a tool made for one Compita, as an MCP server: GitHub with its
token, a database, anything that speaks MCP. In the Compita's panel,
**Connections** takes a name, a command, arguments and environment variables,
one `KEY=value` per line. The command must exist where the Compita runs; the
browser image has Node, so `npx -y @modelcontextprotocol/server-github` works
there.

The secrets stay on this computer: the web UI and the API never send a value
back, and a variable you leave empty when you edit keeps its stored value. The
Compita sees the tools as `mcp_<name>_<tool>`, trusted for what they declare
(read-only, destructive), so a careful Compita asks before a destructive call.
A connection belongs to one Compita; the machine's own MCP servers aren't
copied.

## Compitas talking to each other

Inside a turn, the `message_peer` tool sends a message to another Compita, by
name, through Compa, and returns its reply. That takes as long as the other
Compita's turn. A chain of messages stops after 3 hops, and a Compita that is
busy answers "busy" instead of waiting. Messages appear in the receiving
Compita's chat, and the reply goes back by itself: a Compita answers a
colleague by replying, not by messaging back.

## Security

- Enrollment tokens work once and expire after 15 minutes. Compa keeps them,
  and the credentials of enrolled machines, only as hashes. The dial secret and
  a webhook's token have to be kept as they are, to present the secret and to
  check signatures. `compute.json`, objectives, triggers and chats are readable
  by your account only.
- A machine or a webhook reaches Compa with a credential of its own: an
  enrollment token, the compute's credential, the dial secret, a trigger token
  or signature. Everything else needs your login.
- A Compita on this computer proves itself with a credential derived from its
  own id, so it can't ask for approval or message as another Compita.
- A compute can answer only the jobs it was given. A message from one Compita
  to another reaches the colleague at the address the sender's own turn was
  given, never one named by the message.
- A container can write only its own folder. Compa writes in that folder only
  through a handle that cannot leave it, and replaces a link left where it
  keeps a file, so a Compita can't make Compa write elsewhere with one.
- Dial in uses a bearer secret; use `--tls` and the pin. There are no client
  certificates.

## Limits

- Free providers throttle (HTTP 429 and quota errors); turns slow down but
  continue. Connect a provider with a key for steady work.
- A Compita holds the model and tool keys of its machine, in its own folder. A
  container limits what it can reach on the machine, not what it can send out
  over the network.
- Containers on one machine share Docker's default network, and a browser's
  screen has no password of its own, so another container on that machine could
  reach a Compita's screen.
- The install line is for Linux and macOS. A Windows machine can be a compute:
  install `compa-kernel` there and run the two commands above.
