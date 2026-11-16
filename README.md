# netbirdpod

A terminal console for the [NetBird](https://netbird.io) client on your
machine: its profiles (connections), this peer, and the peers it can reach,
with ssh, remote desktop and nmap scans one key away.

> Unofficial project, not affiliated with or endorsed by NetBird.

netbirdpod drives the installed `netbird` CLI, so it needs a working NetBird
installation (daemon running, at least one profile) and nothing else: no API
token, no management server access.

## Tabs

| Tab | Shows | Keys |
|---|---|---|
| 0 Connections | Every NetBird profile: account, and for the active one its state, management server, this peer's name and IP, connected peers, session expiry | `u` up, `D` down, `L` login |
| 1 This peer | FQDN, IPv4/IPv6, public key, WireGuard interface and port, management and signal servers, session expiry, lazy connections, quantum resistance, SSH server, relays, routes, nameservers, versions, recent events | scroll |
| 2 Peers | Every peer: status, P2P or relayed, latency, last handshake, traffic, routes it offers, open ports of the last scan | `s` / `S` ssh, `d` RDP, `n` / `N` nmap, `c` connected only |

`enter` opens the detail of a row (`esc` back). A peer's detail lists its
connection (ICE candidates and endpoints, relay, handshake), the exact ssh
and RDP command lines, and the full nmap result. `F1` or `?` lists every key.

NetBird reports the state of the active profile only, so other profiles are
listed by name and account. `u` on one of them switches to it: when another
profile is connected netbirdpod asks, then runs `netbird down`,
`netbird profile select` and `netbird up --profile`.

`netbird up` and `netbird login` may need an SSO login, so they run in the
terminal with netbirdpod suspended: the login URL (and the browser NetBird
opens) stay visible. On failure the output stays on screen until `enter`.

Everything on screen refreshes every minute (the `refresh` setting); `a`
turns auto-refresh off or on per tab, `r` reloads now.

## ssh

`s` opens ssh to the peer where `ssh_mode` says (default `tab`); `S` asks:

| Mode | Opens |
|---|---|
| `here` | in netbirdpod's own terminal; netbirdpod comes back when ssh exits. Works everywhere |
| `window` | a new terminal window |
| `tab` | a new tab of the terminal netbirdpod runs in |
| `split-right` | a pane next to netbirdpod |
| `split-down` | a pane below netbirdpod |

netbirdpod finds the terminal it runs in from its parent processes, or else
from the variables the terminal sets. A multiplexer (tmux, Zellij) wins over
the terminal showing it. The supported terminals:

| Terminal (desktop) | Tab | Split right / down | New window |
|---|---|---|---|
| Konsole (KDE) | D-Bus, see below | D-Bus, see below | `konsole -e` |
| GNOME Terminal (GNOME, Cinnamon, Unity) | `gnome-terminal --tab --` | – | `gnome-terminal --` |
| Ptyxis (GNOME 47+) | `ptyxis --tab --` | – | `ptyxis --new-window --` |
| Console / kgx (GNOME) | `kgx --tab --` | – | `kgx --` |
| Xfce Terminal (Xfce) | `xfce4-terminal --tab -x` | – | `xfce4-terminal --window -x` |
| MATE Terminal (MATE) | `mate-terminal --tab -x` | – | `mate-terminal --window -x` |
| Tilix (Budgie) | `tilix --action=app-new-session` | `--action=session-add-right` / `-down` | `tilix -e` |
| Terminator | `terminator --new-tab` (first window) | `remotinator vsplit` / `hsplit` | `terminator -x` |
| elementary Terminal (Pantheon) | `io.elementary.terminal -t -x` | – | `-n -x` |
| QTerminal (LXQt), LXTerminal (LXDE), COSMIC Terminal, Deepin Terminal, Alacritty, Ghostty, foot, xterm | – | – | each one's `-e` / `--` |
| WezTerm | `wezterm cli spawn` | `wezterm cli split-pane --right` / `--bottom` | `wezterm start` |
| kitty | `kitten @ launch --type=tab` | `--location=vsplit` / `hsplit` (in the splits layout) | `kitty` |
| tmux | `tmux new-window` | `tmux split-window -h` / `-v` | the terminal tmux runs in |
| Zellij | – | `zellij run --direction right` / `down` | the terminal Zellij runs in |
| iTerm2 (macOS) | AppleScript `create tab` | `split vertically` / `horizontally` | `create window` |
| Terminal (macOS) | – | – | AppleScript `do script` |
| Windows Terminal | `wt -w 0 new-tab` | `wt -w 0 split-pane -V` / `-H` | `wt -w new new-tab` |

kitty needs remote control (`allow_remote_control` and a `listen_on`
socket in kitty.conf).

For a new window, netbirdpod tries in order: the `terminal` setting, the
terminal it runs in, `$TERMINAL`, the default terminal of the desktop
(`$XDG_CURRENT_DESKTOP`, e.g. Konsole on KDE, Ptyxis or GNOME Terminal on
GNOME, xfce4-terminal on Xfce), `x-terminal-emulator`, and then any known
terminal that is installed.

A tab or split the terminal cannot open falls back to a new window. The `S`
menu marks such modes with `→window` and says why before you choose.

### Konsole

Konsole has no command-line option to open a tab or split *in the current
window* with a command (`konsole --new-tab -e` reuses a window only with
"Run all Konsole windows in a single process" and is documented as unreliable
with `-e`). Its D-Bus interface can, and netbirdpod uses it on the window it
runs in (`$KONSOLE_DBUS_SERVICE`, `$KONSOLE_DBUS_WINDOW`,
`$KONSOLE_DBUS_SESSION`):

- **tab**: `Window.newSession`, then `Session.runCommand "ssh …"` in it;
- **split**: `Window.createSplit` on netbirdpod's own view (side by side for
  `split-right`, stacked for `split-down`), then `runCommand` in the new pane;
- the tab title is set to the peer name.

Konsole only allows `runCommand` with **Settings → Configure Konsole →
General → "Enable the security sensitive parts of the DBus API"** turned on
(it is off by default, because any local program could then type into your
shells). netbirdpod checks this first; when it is off, tab and split open a
new Konsole window (`konsole -e ssh …`) instead, which always works.

So for Konsole users:

- want tabs/splits: enable that option, and set `"ssh_mode": "tab"` or
  `"split-right"`;
- prefer not to: use `"ssh_mode": "window"`, or `here`, which keeps ssh in
  the same tab and returns to netbirdpod on exit;
- inside tmux in Konsole, tmux is used and the Konsole option does not matter.

ssh connects to the peer's NetBird IP (or its FQDN with `"use_fqdn": true`,
which needs NetBird DNS). Set the user globally (`ssh_user`) or per peer; to
use NetBird's own SSH server, set `"ssh_command": ["netbird", "ssh"]`.

## Remote desktop

`d` starts an RDP client to the peer, the first found of:

| Client | Started as |
|---|---|
| `rdp_command` setting | the template, with `{host}`, `{user}`, `{name}` |
| KRDC | `krdc rdp://user@host` |
| Remmina | `remmina -c rdp://user@host` |
| FreeRDP (`xfreerdp3`, `sdl-freerdp3`, `xfreerdp`, `sdl-freerdp`, `wlfreerdp`) | `/v:host /u:user /dynamic-resolution /cert:tofu`, in a terminal (`rdp_mode`, default a new window) so it can ask for the password |
| macOS | `open rdp://…` (Microsoft Remote Desktop / Windows App) |
| Windows | `mstsc /v:host` |

Arguments with `{user}` are dropped when no user is configured.

## Scans

`n` runs a quick nmap scan of the peer's NetBird IP (`-Pn -T4 -F`: the 100
most common ports), `N` a full one (`-Pn -T4 -sV --top-ports 1000`: service
and version detection, slower). Scans run in the background; open ports show
in the Peers table and the full result in the peer's detail. Both argument
lists can be changed in the configuration. Peers with lazy connections are
idle until traffic flows; a scan brings the tunnel up.

## Install

Download a binary from the release, or:

```sh
go install github.com/pvanek/netbirdpod/cmd/netbirdpod@latest
```

Needs `netbird` on the `PATH` (or the `netbird` setting); `nmap`, `ssh` and an
RDP client only for their features.

## Configuration

Optional. netbirdpod reads `-config PATH`, else `$NETBIRDPOD_CONFIG`, else
`netbirdpod/config.json` in the user configuration directory
(`~/.config` on Linux). Every field is optional:

```json
{
  "refresh": "1m",

  "ssh_mode": "tab",
  "ssh_user": "admin",
  "ssh_command": ["ssh", "-A"],
  "use_fqdn": false,
  "terminal": ["konsole", "-e"],

  "rdp_user": "Administrator",
  "rdp_command": ["xfreerdp3", "/v:{host}", "/u:{user}", "/dynamic-resolution"],
  "rdp_mode": "window",

  "nmap_quick": ["-Pn", "-T4", "-F"],
  "nmap_full": ["-Pn", "-T4", "-sV", "--top-ports", "1000"],

  "peers": {
    "kestra-prd": { "ssh_user": "ubuntu" },
    "win-desktop": { "rdp_user": "petr" },
    "gitlab": { "host": "gitlab.internal" }
  },

  "netbird": "netbird",
  "nmap": "nmap"
}
```

| Field | Meaning |
|---|---|
| `refresh` | auto-refresh interval, at least `5s` (default `1m`) |
| `ssh_mode` | `here`, `window`, `tab`, `split-right` or `split-down` (default `tab`) |
| `ssh_user`, `ssh_command` | ssh login and command; `[user@]host` is appended |
| `use_fqdn` | connect to NetBird FQDNs instead of IPs |
| `terminal` | new-window command, the command line is appended (default: detected) |
| `rdp_user`, `rdp_command`, `rdp_mode` | RDP login, client template and where terminal clients open |
| `nmap_quick`, `nmap_full` | nmap arguments of `n` and `N` |
| `peers` | per-peer `ssh_user`, `rdp_user` and `host` (address override), keyed by peer name, FQDN or IP |
| `netbird`, `nmap` | binaries, if not on the `PATH` |

## Command line

```
netbirdpod            start the console
netbirdpod status     print this peer and its peers once
netbirdpod -version
```

## Build

```sh
make          # bin/netbirdpod
make test     # vet and tests
make dist     # Linux, Windows and macOS binaries plus SHA256SUMS in dist/
```

`NETBIRDPOD_LIVE=1 go test ./internal/tui -run Live -v` renders every screen
with the local NetBird client and scans this machine's own NetBird IP.
