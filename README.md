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
| `a` | Add a connection, picking an alias from `~/.ssh/config` |
| `e` | Edit the selected connection: label, SSH target, remote session |
| `space` | Connect or disconnect |
| `enter` | Show what the command behind that connection is doing |
| `d` | Forget it, removing it from herdr too |
| `R` | Reload |
| `q` | Close the popup |

Connecting runs `herdr machine add`, which prepares the remote host and can take
minutes. It runs in the background: the row shows `connecting…`, the popup can
be closed, and a herdr toast reports the result. If the command asks something
the plugin was not told to answer — a password, an unexpected confirmation — the
row turns into `needs an answer` and `enter` opens the place to type it.

The form asks up front whether installing herdr on a remote that lacks it is
allowed, and that answer is what the plugin replies with when `machine add`
asks. Passwords are never answered on your behalf.

A machine added outside the plugin, with `herdr machine add` on a command line,
is adopted into the list rather than ignored.

## Configuration

Optional, in `config.toml` inside the directory
`herdr plugin config-dir herdr.machine-manager` prints. `config.example.toml`
in this repository lists the same settings.

| Setting | Default | Effect |
| --- | --- | --- |
| `popup_width`, `popup_height` | `"55%"`, `"50%"` | Popup size. A quoted percentage or a bare cell count. |
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
