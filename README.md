# desk-agent

A small background agent that tells my portfolio site, live, what my laptop is doing:
whether I'm at the keyboard, which app is in front, which repo I'm working in, whether
Claude Code is busy, what's playing. It's what powers the "right now" panel at
[onkaryaglewad.in](https://www.onkaryaglewad.in).

If you're reading this because you want to run your own: clone it, point it at your own
endpoint (see [Configuration](#configuration)), and everything below applies.

It is built so that it **cannot** leak the things I care about. Read
[What is sent and what is never sent](#what-is-sent-and-what-is-never-sent) first; that's the
point of the project.

## The stack in one line

One static Go binary (`CGO_ENABLED=0`), run as a systemd user service, listens to
Hyprland, the Wayland idle protocol, logind, MPRIS and Claude Code hooks, and `POST`s a
tiny JSON sample to `https://www.onkaryaglewad.in/api/ingest/desk`, which the site serves
back at `/api/desk`. A small browser extension adds the current site's hostname.

```
Hyprland socket ─┐
Wayland idle ────┤
logind (lid/sleep)┤
MPRIS (D-Bus) ───┼──►  desk-agent  ──►  offline queue  ──►  POST /api/ingest/desk
Claude Code hooks┤        │
browser extension┘        └─ privacy rules live here, in one place, with tests
```

## What is sent and what is never sent

| Sent | Never sent |
|---|---|
| Whether I'm active, and how long I've been idle (coarse) | Window titles. Ever. |
| The focused app by **name and category** (`Kitty`, terminal) | URLs, paths, page titles, search queries |
| The **repo name** of the focused terminal/editor (`desk-agent`), unless listed in `hide_repos` | File names, file contents, prompts, command lines |
| Whether Claude Code is working, and the repo it is in | Keystrokes, screenshots, clipboard |
| The **site** I'm on by friendly name and category (`GitHub`, code) | Browser history (`scan-history` reads a *copy*, locally, and sends nothing) |
| Native music players: track and artist | Browser media titles. YouTube is "watching something on YouTube" plus a video count; no title, no account |
| Lid closed, battery percent and charging | Anything about adult sites: they collapse to *nothing* |
| How long each app was focused in the last 30 seconds, so a quick flick between workspaces doesn't count as switching what I'm doing | Anything about private/incognito windows |
| How busy the laptop is (CPU and RAM in use), uptime, and what the agent itself costs in memory and CPU | Which processes are running (the only process it looks for is its own settings app, to count its memory) |

How each rule is enforced, not just promised:

- **Titles.** The Hyprland socket is read for the window *class* and *pid* only; the title
  field is never decoded. The smoke test feeds the agent a secret title and checks the wire.
- **Paths and URLs.** The repo name is the last path element of the git root; the path never
  leaves the process. The extension sends a hostname and the native host rejects anything
  else, including messages with extra fields.
- **YouTube.** A YouTube tab (seen via MPRIS, or the extension) becomes
  `source: "youtube"` with a video counter. Titles of browser media are never copied out of
  the D-Bus message, so a second, private account has nothing to leak.
- **Adult sites.** Host-keyword heuristics plus an optional community blocklist. A match
  sends no site at all and the host is never listed in `sites.toml`. The check runs before
  the catalog lookup, so no entry can override it.
- **Private windows.** The extension only reports for a *focused, normal* window; it
  doesn't run in private windows unless you switch that on, and even then reports nothing.
- **Subdomains.** Only the catalog key or the registrable domain is reported, never
  `alice-private-notes.example.com`.
- **The key** lives in `~/.config/desk-agent/key` (mode 600), never in the repo.

A sample on the wire looks like this (every field except `t` and `active` is optional):

```json
{"t":"2026-10-26T14:30:00Z","active":true,"idleS":0,
 "app":{"id":"kitty","name":"Kitty","category":"terminal"},
 "repo":"desk-agent","claude":true,
 "media":{"player":"spotify","state":"playing","source":"spotify","title":"…","artist":"…"},
 "battery":{"pct":81,"charging":false}}
```

and with a browser in front:

```json
{"t":"2026-10-26T14:30:30Z","active":true,"idleS":0,
 "app":{"id":"firefox","name":"Firefox","category":"browser"},
 "site":{"host":"github.com","name":"GitHub","category":"code"},
 "media":{"player":"firefox","state":"playing","source":"youtube","videos":3}}
```

`t` is floored to the heartbeat interval so retries and bursts share one idempotency key.

## Install (Arch Linux + Hyprland)

```sh
sudo pacman -S --needed go git
git clone https://github.com/once-human/desk-agent
cd desk-agent
./install.sh
```

The installer builds the binary into `~/.local/bin`, creates the config, asks for the site key
(the value of the site's `ADMIN_SECRET`; input hidden), installs and starts the systemd user
service, merges the Claude Code hooks, writes the browser native-messaging manifests, builds
the extension and runs `desk-agent doctor`. It is safe to run again. Options: `--no-hooks`,
`--no-browser`, `--no-service`, `--no-gui`, `--uninstall` (keeps your config), `--uninstall --purge`.

Then, once:

```sh
desk-agent scan-history     # propose sites.toml from browser history; opens $EDITOR to review
desk-agent scan-apps        # after a day of use: propose apps.toml from .desktop files
desk-agent fetch-blocklist  # optional: community adult-site blocklist, fetched once
```

and load the browser extension: [`extension/README.md`](extension/README.md).

Check everything at any time:

```sh
desk-agent doctor
```

It tests every signal source and says what works. It prints the focused app *class* only,
never a title.

### The app (GTK4 + libadwaita)

```sh
sudo pacman -S --needed python-gobject gtk4 libadwaita   # librsvg is normally already installed
./install.sh                                             # installs "Desk Agent" into your launcher
desk-agent-gui                                           # or run it from a terminal
```

The app is a window onto the same agent, for people who would rather click than edit TOML.
If the GTK libraries are missing, the installer skips the app and says what to install; the
command line keeps working either way.

| Page | What it is for |
|---|---|
| **Live** | What the site is being told right now, in plain words, with the exact JSON the agent would send. A pause button, a private-mode switch, delivery status, today's timeline, and the site's own public view of you. |
| **Privacy** | Private mode, how long you count as active, which repos are hidden, and a one-line promise of what is never sent. |
| **Library** | Your sites and apps: search, rename, recategorise, hide, add. "Scan browser history" proposes sites for you to tick; only host names and counts are read, never URLs or titles, and adult sites cannot be listed. |
| **Health** | Every signal source with a green / amber / red verdict (the same checks as `desk-agent doctor`), the service, the site key, the extension bridge. |

It follows Material You: pick a seed colour (a few presets or any colour) and the whole window
re-tones for light and dark. It is an approximation built on OKLCH, not Google's exact HCT
algorithm, so colours will differ a little from what `matugen` or Android would give you from
the same seed. On a narrow window the side rail becomes a bottom bar.

The app holds no secrets and sends nothing itself. Every change goes through the same
`desk-agent` commands you could type (so the same validation and the same adult-site refusal
apply), and the running agent applies them **immediately**: editing sites, apps, privacy or the
blocklist no longer needs a restart. The one exception is `endpoint`, which is deliberately not
settable from the app or the CLI; edit `config.toml` and restart.

### Pause from Hyprland

```ini
# hyprland.conf
bind = SUPER SHIFT, P, exec, desk-agent toggle
```

Pausing sends one final "not active" sample, then nothing until resumed. It survives restarts.

### Browsers

Chromium family (Chrome, Chromium, Brave, Vivaldi, Edge): load `extension/dist/chromium`
unpacked. Firefox family: release Firefox only runs signed add-ons permanently; see
[`extension/README.md`](extension/README.md) for the options (temporary load, AMO unlisted
signing, or Developer Edition).

## Commands

| Command | What it does |
|---|---|
| `desk-agent run` | Run the agent (the service does this) |
| `desk-agent-gui` | The app: live view, privacy, sites and apps, health |
| `desk-agent doctor` | Check every signal source (`--json` for scripts) |
| `desk-agent config show` / `config set <key> <value>` | Read and change settings; a running agent picks them up at once |
| `desk-agent sites list` / `set` / `remove` / `import` | Manage `sites.toml` without opening it (comments kept, adult hosts refused) |
| `desk-agent apps list` / `set` / `remove` / `import` | The same for `apps.toml` |
| `desk-agent init` | Create `~/.config/desk-agent/config.toml` (never overwrites) |
| `desk-agent set-key` | Store the site key, hidden input, mode 600 |
| `desk-agent pause` / `resume` / `toggle` / `status` | Stop and start sending |
| `desk-agent scan-history` | Propose `sites.toml` from browser history (reads a copy, prints counts only) |
| `desk-agent scan-apps` | Propose `apps.toml` from `.desktop` files of apps you actually focused |
| `desk-agent fetch-blocklist` | Download a community adult-site blocklist once |
| `desk-agent install-hooks` / `uninstall-hooks` | Merge / remove only our Claude Code hooks |
| `desk-agent install-native-host` / `uninstall-native-host` | Browser extension bridge manifests |

## Configuration

`~/.config/desk-agent/config.toml` (created by `init`, all keys optional):

| Key | Default | Meaning |
|---|---|---|
| `endpoint` | the site's ingest URL | Where samples go. Must be `https://` (localhost is allowed for testing) |
| `interval_s` | `30` | Heartbeat while active. Changes are sent immediately regardless |
| `idle_interval_s` | `60` | Heartbeat while idle |
| `active_window_s` | `180` | Input within this many seconds counts as active |
| `private` | `false` | Presence only: no app, site, repo, Claude state, media or battery |
| `hide_repos` | `[]` | Repo names never reported |
| `battery` | `true` | Report battery percent and charging |
| `sites_file`, `apps_file` | in the config dir | Category maps |
| `adult_blocklist_file` | `adult-hosts.txt` in the config dir | Optional extra blocklist |

Environment: `DESK_AGENT_KEY` overrides the key file; `DESK_AGENT_DEBUG=1` turns on debug logs.

### `sites.toml` and `apps.toml`

Data, not code. Nothing about my own sites is hard-coded; the built-in lists are only a
starting seed.

```toml
["monkeytype.com"]          # a key also matches its subdomains; the most specific key wins
name = "Monkeytype"
category = "learning"       # code docs writing mail chat social video stream music design
                            # shopping news learning ai tool private other

["mybank.example"]
hide = true                 # report nothing for this host
```

A host that isn't listed is shown by the friendly name of its registrable domain with
category `other` (`monkeytype.com` → "Monkeytype"). App categories: `editor ide terminal chat
design browser media other`.

## How the signals work

| Signal | Source | Notes |
|---|---|---|
| Focus | Hyprland `.socket2.sock` events, class+pid from `.socket.sock` | Titles are never decoded |
| Idle | Wayland `ext-idle-notify-v1` | Event driven, no polling. Respects idle inhibitors (see below) |
| Lid, suspend, shutdown | logind over the system bus, with a delay inhibitor | The final sample goes out before the machine sleeps |
| Media | MPRIS on the session bus | Browser players: no titles. Native players: track and artist |
| Claude Code | Hooks write a tiny state file; "working" expires 90 s after the last event | Only the session's cwd is kept, locally; only the repo name is sent |
| Repo | Process tree of the focused window (`/proc`), git root basename | Worktrees and submodules handled |
| Site | Browser extension → native messaging → agent socket | Hostname only; expires if the extension goes quiet |

If the site is unreachable, samples are queued in `~/.local/state/desk-agent/queue.jsonl`
(7 days, 5000 lines) and flushed with back-off when it returns.

## Known limits

- **Hyprland only** for focus. On another compositor you still get idle, lid, media and Claude state.
- **Idle inhibitors.** `ext-idle-notify` honours them, so while a fullscreen video or a call holds one,
  you are reported as active even without touching the keyboard.
- **`idleS` is coarse** (10 s, 60 s and the active window), and timestamps are floored to the
  heartbeat interval, so two events in the same 30 s bucket overwrite each other server-side.
- **Flatpak/Snap browsers** sandbox native messaging; not set up by the installer.
- **Firefox** needs a signed add-on for a permanent install.

## Uninstall

```sh
./install.sh --uninstall           # removes the app too; keeps ~/.config/desk-agent
./install.sh --uninstall --purge   # also deletes config, key, queue and state
```

Then remove the extension from the browser.

## Development

```sh
make build test           # or: go build ./... && go test ./...
go test -tags dbustest ./internal/mpris ./internal/logind    # needs dbus-daemon
python3 test/e2e/agent_smoke.py && python3 test/e2e/hooks_smoke.py   # fake Hyprland + fake site
python3 test/e2e/live_reload_smoke.py                                 # config/sites/apps applied without a restart
make gui-test              # app: unit tests + the real agent behind a fake desktop, under Xvfb
```

The hardware-dependent parts are tested against fakes: a fake Hyprland socket, a private
`dbus-daemon` with fake MPRIS and logind services, and a fake ingest server. The GitHub Actions
workflow in `.github/workflows/ci.yml` builds and runs all of that on every push. The app's tests
drive the real window under Xvfb against the real agent, assert on what is on screen and on the
wire (for example that no window title ever appears), and keep screenshots as a CI artifact.
The app has no dependency beyond PyGObject, GTK 4 and libadwaita; it is tested on Python 3.12, GTK 4.14 and libadwaita 1.5. Layout: `internal/<signal>` per source, `internal/engine` decides
what to send, `internal/sample` is the wire format, `internal/{adult,hostname,site,catalog}`
are the privacy rules.

## License

MIT, see [LICENSE](LICENSE).
