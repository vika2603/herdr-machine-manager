# Design

Why this plugin is built the way it is. What it does and how to use it is in
the README; how to build and run it is in the justfile.

Target: herdr 0.9.0 (protocol 22), macOS and Linux. Go client:
`github.com/vika2603/herdr-client`.

## 1. What this has to work with

### 1.1 Saved machines are a CLI-only surface

```
herdr machine list [--json]
herdr machine add <ssh-target> --label <label> [--remote-session <name>]
herdr machine rename <profile-id> --label <label>
herdr machine remove <profile-id>
herdr machine enable|disable <profile-id>
```

None of herdr's socket API methods touches machines: the schema declares no
`machine.*` method and the session snapshot carries workspaces, tabs, panes and
agents only. A plugin that manages machines shells out to `herdr machine`; the
socket API is used for panes and notifications, not for this data.

A saved machine is a `SavedSshEndpoint` with five fields — id, label, SSH
target, explicit remote session, enabled flag — kept in
`~/.config/herdr/endpoints.json`, with the current selection beside it in
`endpoint-selection.json`. `herdr machine list --json` reports them as `id`,
`label`, `target`, `session`, `enabled` and `selected`; a profile id is 32
lowercase hexadecimal characters. These subcommands are parsed by hand, and the
positional argument must precede the flags, whatever the clap-style ordering in
`--help` suggests.

Two properties of that surface shape everything below:

- **`rename` changes the label and nothing else.** No CLI path rewrites the SSH
  target or the remote session of a saved machine. This is the reason the plugin
  keeps its own store rather than editing herdr's.
- **`add` is interactive and slow.** It prepares the remote host before it saves
  anything: it can ask `continue installing the remote herdr binary? [y/N]`, it
  asks before stopping an incompatible remote server (defaulting to No), and the
  underlying `ssh` may prompt for a password or a key passphrase. It is neither
  a silent config write nor a command that reliably finishes in a second.

### 1.2 Entrypoints, popups and the keybinding

A manifest declares `[[startup]]`, `[[actions]]`, `[[events]]`, `[[panes]]` and
`[[link_handlers]]`. herdr launches a startup command once and does not
supervise it: the process stays alive itself, and nothing restarts it.

A keybinding of type `plugin_action` names the action in full, plugin id and
action id joined by a dot, which is the form herdr's own plugin documentation
uses. A bare action id also works, and is what the "matches more than one
action" error refers to.

Popup placement is reachable from the socket API only. `placement = "popup"` is
valid in a manifest — with `width`/`height`, which only popups accept — and
`PluginPanePlacement` includes `popup`, but `herdr plugin pane open --placement`
offers only `overlay`, `split`, `tab` and `zoomed`. So the action opens the pane
through `plugin.pane.open`, that is through `herdr-client`, rather than by
running the CLI. herdr allows one popup at a time; a second open fails with
`ui_busy`.

### 1.3 The plugin process environment

herdr injects `HERDR_PLUGIN_ID`, `HERDR_PLUGIN_ROOT`, `HERDR_PLUGIN_CONFIG_DIR`,
`HERDR_PLUGIN_STATE_DIR`, `HERDR_SOCKET_PATH`, and one of
`HERDR_PLUGIN_ACTION_ID` / `HERDR_PLUGIN_ENTRYPOINT_ID` / `HERDR_PLUGIN_EVENT`.
`plugin.Load` reads them and the registry dispatches to the handler for that
entrypoint.

A pane entrypoint runs until the user closes the pane, which delivers SIGHUP and
then SIGTERM; `plugin.ShutdownContext` ends the context on either. A plugin
process herdr starts is subject to a concurrent-command limit
(`plugin_command_limit_reached`); processes the daemon starts itself are not.

## 2. Decisions

1. **The plugin's own store is the source of truth**, in
   `$HERDR_PLUGIN_STATE_DIR/connections.json`. herdr holds the connections that
   are active and nothing else, so `active` is derived from herdr's list rather
   than stored twice and the two can never disagree.
2. **Only `machine add` and `machine remove` are used.** Disconnecting removes a
   connection from herdr and keeps its configuration here; herdr's own
   `enable`/`disable` are not used at all. The cost is that connecting again
   re-runs `machine add`, which prepares the remote host: cheap when the remote
   already has herdr, never instant.
3. **Editing is a plain edit.** Label, target and session are fields of the
   stored connection. Saving a changed label on an active connection uses
   `machine rename`, the one cheap herdr command; a changed target or session
   queues a disconnect followed by a connect.
4. **A machine added outside the plugin is adopted**, not ignored and never
   deleted: reconciliation imports anything herdr holds that the store does not
   know about.
5. **All mutations go through `herdr machine`.** The plugin never writes
   `endpoints.json`: the format is undocumented, a running client holds it, and
   `add` does remote work no config write reproduces.
6. **Every mutation is a job owned by the resident daemon.** The TUI submits and
   watches; it never runs `herdr machine` itself. Closing the popup leaves a
   running job alone.
7. **`add` runs under a PTY the daemon owns.** That keeps it answerable when it
   asks something, without tying it to the lifetime of a popup.
8. **Jobs are not a user-facing concept.** A job belongs to a connection, so its
   state is shown on that connection's row and its output lives behind that row.
   There is no job list and no job history.

## 3. Architecture

Three manifest entrypoints, one binary:

```
herdr start
  → [[startup]] daemon                    resident, survives popups
        holds the connection store and the job queue

prefix+shift+s
  → plugin_action "herdr.machine-manager.open"
  → [[actions]] open                      short-lived, no TTY
        ensures the daemon is up, then plugin.pane.open { entrypoint: "manager" }
  → [[panes]] manager (placement = popup) a TTY, lives until the popup closes
        the TUI: a client of the daemon
```

The split exists because the three have different lifetimes. The action is a
process herdr reaps in milliseconds. The pane dies when the user presses `q`. A
`machine add` against a fresh host outlives both. Only a resident process can
own the job, and herdr already offers one.

Binding `type = "popup"` straight to the binary would skip the plugin system
entirely. It is rejected because it gives up manifest-driven install and build,
the action would not appear in `herdr plugin action list`, and the binary would
have to resolve its own socket and state directories.

### 3.1 Daemon

- **Connection store and reconciliation.** The store is read at start and
  written on every change. herdr's list is read at start, after every job that
  mutates it, and when `endpoints.json` changes — noticed by polling its
  modification time every two seconds, which needs no watcher dependency and is
  timely enough for an edit the plugin did not make. Reconciliation is
  serialized: the job hook, the poll and an explicit refresh can fire at once,
  and two of them would each decide a machine is unclaimed and adopt it twice.
- **Job queue, one lane per connection.** Jobs of the same connection run in
  order — the disconnect and connect of a reconnect must not overlap — and
  different connections run in parallel, because preparing two remote hosts has
  no reason to be sequential. A lane retires after a minute of quiet, so a
  daemon that outlives many connections does not accumulate goroutines.
- **PTY execution for `connect`.** The command runs under a PTY the daemon
  allocates, at a fixed 120×40, so `ssh` and herdr's own prompts behave as they
  do in a terminal. Output is kept as a bounded tail and broadcast line by line.
- **Notifications.** On a terminal job state, `notification.show` over the
  socket API, so the result reaches the user through the toast configuration
  they already have rather than through a channel this plugin invents.
- **State.** The socket is `manager.sock` in `$HERDR_PLUGIN_STATE_DIR`. A unix
  socket path is limited to about 100 bytes, so a state directory too deep for
  one falls back to a name in the temp directory derived from it.

Single instance: the daemon holds a lock file beside the socket. A second
instance of the same version exits; a newer one asks the older to quit and takes
over, which is what makes a rebuild take effect without restarting herdr. That
request ends the older daemon's context rather than merely closing its listener:
a popup's open subscription would otherwise keep it serving, and its lock held,
until the user closed that popup.

Startup is not supervised, so the daemon can also be missing — herdr was already
running when the plugin was linked, or it crashed. Both the action and the TUI
therefore start it if the socket does not answer, the way the herdr client
spawns its own server. A daemon spawned from a popup is detached with `setsid`
so the popup's SIGHUP does not take it down.

### 3.2 IPC

The wire format *is* herdr's: newline-delimited JSON over a unix socket, a
request carrying `{id, method, params}`, a reply carrying `{id, result}` or
`{id, error: {code, message}}`, and a subscription pushing `{event, data}`
lines. Because it matches, the TUI talks to the daemon with herdr-client's own
`herdr.Client` — `New(socket)`, `Call`, `OpenStream` — and `internal/ipc` holds
only the server half, which herdr-client does not provide (`plugintest.Server`
is a test double: it needs a `testing.TB`, replays scripted replies and does not
serve subscriptions).

| Method | Meaning |
| --- | --- |
| `connections.list` | the merged connections, the jobs the daemon still keeps, and a revision |
| `connections.refresh` | force a reload, then reply with the list |
| `connection.save` | create or update one; replies with the jobs it queued |
| `connection.connect` | `machine add` this connection |
| `connection.disconnect` | `machine remove` it, keeping it in the store |
| `connection.forget` | remove it from herdr, if held, and from the store |
| `aliases.list` | the `~/.ssh/config` aliases, resolved |
| `job.input`, `job.cancel` | answer or stop a job |
| `events.subscribe` | keeps the connection open, streams connection and job events |

`events.subscribe` mirrors the one herdr method that holds its connection open:
the subscription starts when the daemon accepts it and does not replay history,
so the TUI subscribes first and then asks for the list, the ordering
`herdr.OpenSession` uses. A subscriber that stops reading has its events
dropped rather than blocking the daemon: they are advisory, and a client that
misses some re-reads the list.

### 3.3 TUI

A client, and nothing more. It renders from `connections.list`, applies events
as they arrive, and submits requests. No `herdr machine` process is ever a child
of the TUI, so no keystroke waits on one. If the daemon is unreachable it says
so and retries, re-spawning it.

Bubble Tea drives it, without the alternate screen: herdr destroys the popup
pane when it closes, so there is no scrollback to protect, and staying on the
main screen keeps the view readable to `pane.read`, which is how the TUI is
exercised against a real server.

## 4. Connections and reconciliation

A stored connection is `{id, label, target, session, profile_id, created,
updated}`. `profile_id` is the herdr endpoint id while the connection is active
and empty once it is not; nothing else records whether it is active.

Reconciliation merges the store with what herdr reports:

- A stored connection pairs with a machine by endpoint id, falling back to
  target and label — which is what a connection matches on immediately after
  `machine add`, since that command reports no id.
- The endpoint id is recorded for a connection that paired, and cleared for one
  that did not.
- A machine that paired with nothing is adopted as a new connection.
- Nothing is ever deleted from the store by reconciliation.

The file carries a version, and one written by a newer plugin is refused rather
than reinterpreted. Every write goes through a temporary file that is flushed
and renamed.

## 5. Jobs

### 5.1 Fast jobs

`disconnect`, `rename` and `forget` finish in well under a second and need no
input: one `herdr machine remove` or `rename`, plus a store write for `forget`.
They still go through the queue — same path, same events, same failure surface —
and the row shows what is happening until the job reports back.

### 5.2 `connect`

Connecting runs `herdr machine add`. The honest limit: the plugin cannot make it
unattended. It can keep it off the UI thread, let the user walk away, and bring
them back when it needs them.

```
alias picker → form (label, target, remote session, install?)
  → connection.save        store write, returns immediately
  → daemon queues a connect job; the popup is free to close
  → `herdr machine add …` under a PTY
  → output streams to whoever is subscribed
  → on a known prompt: answer from the choice the user already made
  → on an unknown prompt: state = awaiting_input, notification.show
  → terminal state: notification.show, reload and reconcile, which is where the
    connection learns the endpoint id it was given
```

**Prompt handling.** The daemon matches a small, explicit set of expected
prompts against the PTY tail: the install confirmation, and the
incompatible-server replacement question, which keeps herdr's own default of No.
Password and passphrase prompts are never auto-answered. Anything else moves the
job to `awaiting_input` and notifies — the daemon never guesses at an answer it
was not given.

A prompt is the unterminated text output stopped on. It stops being a prompt
when that line ends, not when any output arrives: a command that prints progress
while it waits must not clear the input field the user is typing into.

**Preflight** is designed but not implemented: `ssh -G <target>` to confirm the
alias resolves, `ssh -o BatchMode=yes -o ConnectTimeout=5 <target> true` to
learn whether the host authenticates without a prompt, and
`ssh -o BatchMode=yes <target> 'command -v herdr'` to learn whether the remote
already has herdr. Today the form asks whether installing is allowed and the
daemon replies with that answer.

**Cancellation** signals the process group, and sends SIGKILL after a grace
period: a command that ignores SIGTERM would otherwise hold its connection's
lane for as long as herdr runs. `herdr machine add` saves nothing until the
remote is ready, so a cancelled or crashed job leaves no half-saved machine; it
can leave a partially installed remote binary, which the next attempt reuses.
The connection stays in the store either way — it is simply not active.

### 5.3 What is kept

The queue holds every unfinished job plus the last finished one per connection:
the error a failed row shows, and the output its detail view shows. Everything
older is dropped as soon as a newer job replaces it. There is no history to
browse and no endpoint to read one from — `connections.list` carries that whole
set, because it is small by construction.

Output travels once: the list carries each job's tail, the progress events that
follow carry only state, and the lines arrive separately as they are produced.

## 6. SSH aliases

The alias list is what makes adding a connection cheap: the user picks `deploy`
instead of typing a target. Two sources, each for what it is good at.

**Enumeration — parse `~/.ssh/config`.** OpenSSH has no command that lists host
aliases, so the file is parsed for `Host` lines:

- A `Host` line carries one or more patterns: `Host a b` defines two aliases.
- Patterns with `*` or `?`, and negated patterns (`!host`), are not connectable
  aliases and are skipped.
- `Include` is followed, relative paths resolving against the including file's
  directory, globs expanded, with a depth limit against cycles. A config that
  includes another tool's file — OrbStack's, say — is ordinary, not an edge
  case.
- `Match` blocks are skipped for enumeration: they carry conditions, not
  aliases.
- Keywords are case-insensitive; duplicates across files fold into one entry.

**Details — `ssh -G <alias>`.** The effective `hostname`, `user` and `port` come
from `ssh -G`, which prints the fully resolved configuration, rather than from
reimplementing OpenSSH's precedence rules. It runs for the aliases on screen,
bounded in parallelism, and a failure degrades to showing the alias alone.

The daemon owns this too — parsed on demand with an mtime check — so the picker
opens against a cache rather than a filesystem walk.

## 7. The interface

**List** — one row per stored connection: `●` when herdr holds it, `○` when only
the plugin does, the label, the target and, when that target is an alias, the
endpoint it resolves to. A connection with a job in flight shows that job
instead — `connecting…`, `needs an answer` — and one whose last job failed shows
the error until the next job replaces it. A new connection is written to the
store before its job runs, so it appears immediately, as `connecting…`.

**Alias picker** — filterable, with the resolved host beside each alias and a
mark on the ones already added.

**Form** — the same screen adds and edits. It shows the `herdr machine add`
command it will run, so it is not a black box over herdr's own CLI, and warns
when a change to an active connection's target means reconnecting it.

**Confirm** — for `forget`, saying whether the connection is also being removed
from herdr, and that sessions already running on that host keep running.

**Output** — what the command behind one connection is doing, and where an
answer it is waiting for is typed. It is a line view over the PTY tail with ANSI
stripped, not a terminal emulator; a prompt asking for a password, passphrase,
secret or token switches the input to a masked field.

Every screen is laid out the same way — header, rule, body, rule, help, status —
and the body is cut to the rows the popup actually has. The program does not use
the alternate screen, so a frame taller than the pane would scroll its own top
away.

## 8. Packages

| Path | Contents |
| --- | --- |
| `cmd/machine-manager` | entrypoint registration: startup, action, pane |
| `internal/store` | the connections file, the source of truth |
| `internal/daemon` | reconciliation, job specs, IPC server, single-instance lock |
| `internal/jobs` | job model, queue with one lane per connection, PTY execution |
| `internal/ipc` | the protocol's server half |
| `internal/machines` | the `herdr machine` CLI wrapper |
| `internal/sshconfig` | `~/.ssh/config` parsing and `ssh -G` resolution |
| `internal/config` | the user's `config.toml`, read only |
| `internal/ui` | Bubble Tea model, key handling, rendering |

Every package is testable without herdr running: `machines` and `jobs` against a
fake `herdr` and against real scripts under a PTY, `sshconfig` against fixtures,
`ipc` over a socket with herdr-client as the client, `daemon` over
reconciliation with a fake `herdr`, `ui` over its state derivation.

## 9. Open questions

- **The prompts `herdr machine add` actually writes.** The patterns in §5.2 are
  matched against herdr's own strings, not against a captured session. Until one
  is captured, an unmatched prompt degrades to `awaiting_input` rather than to a
  wrong answer.
- **How long a reconnect takes** when the remote already has herdr, which is
  what decides whether disconnect/connect feels like a toggle or like a task.
- **Whether a running herdr client reflects a machine change without a restart**,
  which decides whether the popup has to say so after a connect.
- **`selected`**, the field herdr reports for the machine currently selected, is
  read but not shown anywhere.
