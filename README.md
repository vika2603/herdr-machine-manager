# Machine Manager

A [Herdr](https://herdr.dev) plugin that manages SSH machines from a popup:
add one by picking an alias out of `~/.ssh/config`, connect and disconnect it,
edit it, and forget it.

The plugin keeps its own list of connections and treats herdr as the set that is
currently active. Disconnecting removes a machine from herdr and keeps its
configuration here, so a connection can be turned off without losing it — and
its SSH target can be edited, which herdr's own `machine rename` cannot do.

Requires herdr 0.9.0 or newer, on macOS or Linux.

## Install

```bash
herdr plugin install vika2603/herdr-machine-manager
herdr plugin link /path/to/this/checkout # or from a working tree
```

Install builds the binary from source when a Go toolchain is present, and
otherwise downloads the binary attached to the release that matches the
manifest version, accepting it only if its SHA-256 matches
[`scripts/checksums.txt`](scripts/checksums.txt) in the checkout. Release
binaries are built by the `release` workflow for macOS and Linux on amd64 and
arm64. `herdr plugin link` runs no build command; build the working tree
yourself with `just build`.

Then bind a key in `~/.config/herdr/config.toml` and reload:

```toml
[[keys.command]]
key = "prefix+shift+s"
description = "Manage machines"
type = "plugin_action"
command = "herdr.machine-manager.open"
```

```bash
herdr server reload-config
```

## Using it

The popup has one screen: the connections on the left, and on the right a
panel for the selected one with its settings, the address its SSH target
resolves to, its state and the output of its latest job. Adding, editing,
forgetting and answering a question take over the panel while the list stays
in view. In a popup narrower than 76 columns the panel replaces the list, and
`enter` opens the selected connection's details.

Every state has one glyph, one colour and one word, used wherever it appears:
`●` connected, `○` disconnected, `◌` queued, `◐` connecting or another job
running, `◆` needs answer, `✕` failed (with the error); the panel's last job
can also show `✓` succeeded or `⊘` cancelled. The header counts connections by
state. The bottom line lists the keys that apply right now; in a narrow popup
it keeps the most important.

| Key | Action |
| --- | --- |
| `↑` / `↓` or `k` / `j`, `PageUp` / `PageDown`, `Home` / `End` | Move through connections |
| `space` | Connect or disconnect; not while a job is pending |
| `enter` | Answer the selected connection's question; in a narrow popup, open its details |
| `a` | Add a connection, picking an alias from `~/.ssh/config` |
| `e` | Edit label, SSH target, remote session and the install switch |
| `d` | Forget the connection, after a confirmation |
| `x` | Cancel the selected connection's unfinished job |
| `r` | Re-read herdr's machine list |
| `esc` | Close the popup, or step back from details, the alias picker, a form, the forget confirmation or a question |

In the alias picker, typing filters by alias or host, `↑` / `↓` move, `enter`
picks. Aliases already added are marked. When no alias matches, `enter` starts
the form with the typed text, such as `user@host`, as SSH target and label. In
the form, `tab` / `shift+tab` or `↓` / `↑` move between fields, `space` toggles
the install switch, `enter` saves. The form shows the `herdr machine add`
command it will run and warns when saving disconnects and reconnects an active
connection. The forget confirmation says whether the connection is also removed
from herdr; `enter` forgets, `esc` keeps it.

Connecting runs `herdr machine add`, which prepares the remote host and can take
minutes. It runs in the background: the popup can be closed, and a herdr toast
reports the result. When the command asks a question, an open manager shows it
in the panel; otherwise a small standalone popup opens with the question alone
and closes after the last answer or dismissal. Questions come one at a time. A
confirmation starts on No and `←` / `→` choose; a password is masked. `esc` sets
a question aside without answering or cancelling the job; it does not reopen by
itself until a different question arrives, and `enter` on the connection opens
it again. A question that arrives while you type in the alias picker or the form
waits: the header counts it, `ctrl+o` opens it, and it opens by itself once you
save the form or leave the form or the picker.

The install switch decides whether installing herdr on a remote that lacks it is
allowed. Connecting from the list with `space` follows the `install_remote`
setting; the form's switch overrides it for the connect that saving starts. When
allowed, the installation still asks for confirmation; when off, the daemon
declines it. Replacing an incompatible remote server also asks. Passwords are
never answered on your behalf.

A machine added outside the plugin, with `herdr machine add` on a command line,
is adopted into the list rather than ignored.

## Configuration

Optional, in `config.toml` inside the directory
`herdr plugin config-dir herdr.machine-manager` prints. `config.example.toml`
in this repository lists the same settings.

| Setting | Default | Effect |
| --- | --- | --- |
| `popup_width`, `popup_height` | `"55%"`, `"50%"` | Manager popup size. The standalone input popup uses its compact manifest size. |
| `install_remote` | `true` | Allows installation for list connections and sets the form's initial choice. |
| `ssh_config` | OpenSSH's default | Where the aliases are read from. |
| `notifications` | `true` | Whether a finished or blocked job raises a herdr toast. |

## Where things are kept

The connections live in `connections.json` under the plugin's state directory
(`~/.local/state/herdr/plugins/herdr.machine-manager` by default), next to the
daemon's socket and its log. A rebuilt daemon takes over on the next open
once all background jobs have finished, so upgrades leave running commands alive.
herdr's own saved machines stay in
`$XDG_STATE_HOME/herdr/client/endpoints.json`
(`~/.local/state/herdr/client/endpoints.json` by default); this plugin only adds
to and removes from that list through `herdr machine`.

## Development

```bash
just check   # build, test with -race, vet and lint
just link    # build and point herdr at this working tree
just open    # open the popup without pressing the key
just logs    # what herdr recorded about each plugin command
```

`just --list` has the rest. `docs/design.md` explains why the plugin is built
the way it is.

## Releases

```bash
gh workflow run release.yml -f version=0.1.2
```

The workflow builds the binary for every supported platform, writes the version
into the manifest, commits the checksums `scripts/build.sh` verifies against,
tags that commit and publishes the release.

## License

MIT. See [LICENSE](LICENSE).
