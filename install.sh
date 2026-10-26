#!/usr/bin/env bash
# desk-agent installer. Safe to run again: every step is idempotent.
#
#   ./install.sh                 build, install, enable the service, set up hooks and the browser bridge
#   ./install.sh --no-hooks      skip the Claude Code hooks
#   ./install.sh --no-browser    skip the browser native-messaging host
#   ./install.sh --no-service    install the binary only
#   ./install.sh --uninstall     stop and remove everything it installed (keeps your config and key)
#   ./install.sh --purge         with --uninstall: also delete config, key, queue and state
#
# Environment: PREFIX (default ~/.local) decides where the binary goes (PREFIX/bin).
set -euo pipefail

cd "$(dirname "$0")"
REPO_DIR="$(pwd)"
PREFIX="${PREFIX:-$HOME/.local}"
BIN_DIR="$PREFIX/bin"
BIN="$BIN_DIR/desk-agent"
UNIT_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
UNIT="$UNIT_DIR/desk-agent.service"
CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/desk-agent"
STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/desk-agent"
DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/desk-agent"

HOOKS=1 BROWSER=1 SERVICE=1 UNINSTALL=0 PURGE=0
for a in "$@"; do
  case "$a" in
    --no-hooks) HOOKS=0 ;;
    --no-browser) BROWSER=0 ;;
    --no-service) SERVICE=0 ;;
    --uninstall) UNINSTALL=1 ;;
    --purge) PURGE=1 ;;
    -h|--help) sed -n '2,13p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown option: $a (try --help)" >&2; exit 2 ;;
  esac
done

say()  { printf '\033[1m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[33mwarning:\033[0m %s\n' "$*" >&2; }
have() { command -v "$1" >/dev/null 2>&1; }
user_systemd() { have systemctl && systemctl --user show-environment >/dev/null 2>&1; }

# ------------------------------------------------------------------ uninstall
if [ "$UNINSTALL" = 1 ]; then
  say "Uninstalling desk-agent"
  if user_systemd; then
    systemctl --user disable --now desk-agent.service 2>/dev/null || true
  fi
  if [ -x "$BIN" ]; then
    "$BIN" uninstall-hooks 2>/dev/null || true
    "$BIN" uninstall-native-host 2>/dev/null || true
  fi
  rm -f "$UNIT"
  user_systemd && systemctl --user daemon-reload || true
  rm -f "$BIN"
  rm -rf "$DATA_DIR"
  if [ "$PURGE" = 1 ]; then
    say "Purging config, key, queue and state"
    rm -rf "$CONFIG_DIR" "$STATE_DIR"
  else
    echo "Kept $CONFIG_DIR (config, key, sites.toml, apps.toml) and $STATE_DIR. Use --purge to delete them."
  fi
  echo "Remember to remove the browser extension from your browser."
  exit 0
fi

# -------------------------------------------------------------------- build
have go || { echo "Go is required to build desk-agent. On Arch: sudo pacman -S go" >&2; exit 1; }
VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
say "Building desk-agent $VERSION"
mkdir -p "$BIN_DIR"
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$BIN.new" .
mv -f "$BIN.new" "$BIN"
case ":$PATH:" in *":$BIN_DIR:"*) ;; *) warn "$BIN_DIR is not on your PATH; add it to use the desk-agent command" ;; esac

# ------------------------------------------------------------------- config
say "Config"
"$BIN" init
if [ ! -s "$CONFIG_DIR/key" ] && [ -z "${DESK_AGENT_KEY:-}" ]; then
  if [ -t 0 ]; then
    echo "The site key is the same value as the website's ADMIN_SECRET."
    echo "Paste it now (input is hidden, stored in $CONFIG_DIR/key with mode 600), or press Enter to skip:"
    "$BIN" set-key || warn "no key stored; run 'desk-agent set-key' later"
  else
    warn "no site key yet. Run:  desk-agent set-key   (or set DESK_AGENT_KEY)"
  fi
fi

# ------------------------------------------------------------------ service
if [ "$SERVICE" = 1 ]; then
  say "systemd user service"
  mkdir -p "$UNIT_DIR"
  sed "s|@BINARY@|$BIN|g" contrib/desk-agent.service > "$UNIT.new"
  mv -f "$UNIT.new" "$UNIT"
  if user_systemd; then
    systemctl --user daemon-reload
    systemctl --user enable desk-agent.service
    systemctl --user restart desk-agent.service
    sleep 1
    systemctl --user is-active --quiet desk-agent.service \
      && echo "desk-agent.service is running" \
      || warn "service did not stay up; see: journalctl --user -u desk-agent -n 50"
  else
    warn "no systemd user session reachable; unit written to $UNIT but not enabled"
  fi
fi

# -------------------------------------------------------------------- hooks
if [ "$HOOKS" = 1 ]; then
  say "Claude Code hooks (merged into ~/.claude/settings.json; your own hooks are kept)"
  "$BIN" install-hooks --binary "$BIN" || warn "could not install hooks; run 'desk-agent install-hooks' yourself"
fi

# ------------------------------------------------------------------ browser
if [ "$BROWSER" = 1 ]; then
  say "Browser bridge"
  "$BIN" install-native-host --binary "$BIN" || warn "could not write native-messaging manifests"
  if [ -x extension/build.sh ]; then
    extension/build.sh
    echo "Load the extension: see $REPO_DIR/extension/README.md"
    echo "  Chromium family: chrome://extensions -> Developer mode -> Load unpacked -> $REPO_DIR/extension/dist/chromium"
    echo "  Firefox:         about:debugging -> Load Temporary Add-on -> $REPO_DIR/extension/dist/firefox/manifest.json"
  fi
fi

# ------------------------------------------------------------------- finish
say "Doctor"
"$BIN" doctor || true

cat <<NEXT

Next:
  desk-agent scan-history    build sites.toml from your browser history (you review it)
  desk-agent scan-apps       build apps.toml from the apps you actually used (after a day of use)
  desk-agent fetch-blocklist optional community adult-site blocklist
  Pause from Hyprland: bind = SUPER SHIFT, P, exec, desk-agent toggle
NEXT
