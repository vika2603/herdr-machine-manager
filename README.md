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

The popup opens on the connection list. `●` means herdr currently holds the
connection, `○` means only this plugin does.

| Key | Action |
| --- | --- |
| `↑` / `↓`, `Ctrl+P` / `Ctrl+N` | Move through connections |
| `PageUp` / `PageDown` | Move by one visible page |
| `Home` / `End` | Jump to the first / last connection |
| `Ctrl+A` / `Insert` | Add a connection, picking an alias from `~/.ssh/config` |
| `Ctrl+E` | Edit the selected connection: label, SSH target, remote session |
| `space` | Connect or disconnect |
| `Enter` | Show the selected connection's details and latest command output |
| `Delete` | Open the forget confirmation; `Enter` confirms, `Esc` cancels |
| `Ctrl+R` | Reload |
| `Esc` | Close the popup |

The alias picker supports arrows, `Ctrl+N` / `Ctrl+P`, and paging while you type
to filter. `Ctrl+A` / `Ctrl+E` move to the start / end of the filter text.
`Ctrl+Home` / `Ctrl+End` jump to the first / last alias; plain `Home` / `End`
move within the filter text. In forms, `Tab` / `Shift+Tab`, `↓` / `↑`, or
`Ctrl+N` / `Ctrl+P` move between fields. `Space` toggles the focused checkbox;
`Enter` or `Ctrl+S` saves. `Esc` returns to the screen that opened the form.

In connection details, `Ctrl+E` opens the edit form when no answer is being
typed. `Ctrl+X` cancels an unfinished job, including one waiting for input.
Confirmation dialogs use `Tab` or arrows to select No / Yes and `Enter` to
submit; No is initially selected. Password dialogs hide the typed characters.
In input dialogs, `Ctrl+A` / `Ctrl+E` move within the answer. `Esc` dismisses a
dialog for later without answering or cancelling the job; select that connection
and press `Enter` to reopen it. `Ctrl+C` closes the popup from any screen.
The earlier letter shortcuts remain available for compatibility. The footer
shows only the most common actions on one line, with fewer hints in narrow
popups. All shortcuts listed above remain available.

Connecting runs `herdr machine add`, which prepares the remote host and can take
minutes. It runs in the background: the row shows `connecting…`, the popup can
be closed, and a herdr toast reports the result. When the command needs a
confirmation, password or another answer, a compact standalone input popup opens
without the manager list. It identifies the connection and closes after the last
answer or dismissal. If the manager is already open, it shows the question in
place. Multiple questions are shown one at a time. Dismissing a question does
not repeatedly reopen it.

The form asks up front whether installing herdr on a remote that lacks it is
allowed. When allowed, the actual installation still asks for confirmation;
when disabled, the daemon declines installation. Replacing an incompatible
remote server also requires confirmation. Passwords are never answered on your
behalf.

A machine added outside the plugin, with `herdr machine add` on a command line,
is adopted into the list rather than ignored.

## Configuration

Optional, in `config.toml` inside the directory
`herdr plugin config-dir herdr.machine-manager` prints. `config.example.toml`
in this repository lists the same settings.

| Setting | Default | Effect |
| --- | --- | --- |
| `popup_width`, `popup_height` | `"55%"`, `"50%"` | Manager popup size. The standalone input popup uses its compact manifest size. |
| `install_remote` | `true` | Starting position of the install switch in the form. |
| `ssh_config` | OpenSSH's default | Where the aliases are read from. |
| `notifications` | `true` | Whether a finished or blocked job raises a herdr toast. |

## Where things are kept

The connections live in `connections.json` under the plugin's state directory
(`~/.local/state/herdr/plugins/herdr.machine-manager` by default), next to the
daemon's socket and its log. herdr's own saved machines stay where herdr keeps
them, in `~/.config/herdr/endpoints.json`; this plugin only adds to and removes
from that list through `herdr machine`.

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
