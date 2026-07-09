#!/usr/bin/env bash
# weekstat installer — builds the daemon, installs it under ~/.claude/tools/weekstat,
# and registers a systemd --user service that starts on boot.
#
# Usage:  ./install.sh            # build + install + enable service
#         ADDR=127.0.0.1:9000 ./install.sh   # custom dashboard address
#         ./install.sh --tray     # also build+autostart the GNOME/AppIndicator tray
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOOL_DIR="$HOME/.claude/tools/weekstat"
ADDR="${ADDR:-127.0.0.1:7457}"
VERSION="$(git -C "$REPO_DIR" describe --tags --always 2>/dev/null || echo dev)"
WANT_TRAY="${WEEKSTAT_TRAY:-0}"
case " $* " in *" --tray "*) WANT_TRAY=1 ;; esac

command -v go >/dev/null || { echo "Go is required (https://go.dev/dl/)"; exit 1; }

echo ">> building weekstat ($VERSION)"
mkdir -p "$TOOL_DIR"
( cd "$REPO_DIR" && go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$TOOL_DIR/weekstat" . )

echo ">> installing systemd --user service"
UNIT_DIR="$HOME/.config/systemd/user"
mkdir -p "$UNIT_DIR"
sed "s|127.0.0.1:7457|$ADDR|" "$REPO_DIR/systemd/weekstat.service" > "$UNIT_DIR/weekstat.service"

if command -v systemctl >/dev/null && systemctl --user show-environment >/dev/null 2>&1; then
  systemctl --user daemon-reload
  systemctl --user enable --now weekstat.service
  loginctl enable-linger "$USER" >/dev/null 2>&1 || \
    echo "   (could not enable linger — the service won't survive logout; see README)"
  echo ">> service started: systemctl --user status weekstat"
else
  echo ">> systemd --user not available. Run it manually:"
  echo "   $TOOL_DIR/weekstat --addr $ADDR &"
fi

if [ "$WANT_TRAY" = "1" ]; then
  echo ">> building weekstat-tray (GNOME/AppIndicator indicator)"
  ( cd "$REPO_DIR/tray" && go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$TOOL_DIR/weekstat-tray" . )
  # Install the launcher in BOTH the app menu (so you can reopen it after Quit
  # by searching "weekstat" in Activities) and autostart (so it starts on login).
  APPS="$HOME/.local/share/applications"
  AUTOSTART="$HOME/.config/autostart"
  mkdir -p "$APPS" "$AUTOSTART"
  DESKTOP="[Desktop Entry]
Type=Application
Name=weekstat tray
Comment=Claude Code weekly quota indicator
Exec=$TOOL_DIR/weekstat-tray --addr $ADDR
Icon=utilities-system-monitor
Categories=Utility;
X-GNOME-Autostart-enabled=true"
  printf '%s\n' "$DESKTOP" > "$APPS/weekstat-tray.desktop"
  printf '%s\n' "$DESKTOP" > "$AUTOSTART/weekstat-tray.desktop"
  update-desktop-database "$APPS" 2>/dev/null || true
  if command -v gdbus >/dev/null && gdbus introspect --session --dest org.kde.StatusNotifierWatcher --object-path /StatusNotifierWatcher >/dev/null 2>&1; then
    pkill -x weekstat-tray 2>/dev/null || true
    setsid "$TOOL_DIR/weekstat-tray" --addr "$ADDR" >/dev/null 2>&1 &
    echo ">> tray started and set to autostart on login"
  else
    echo ">> tray installed (autostart). No StatusNotifierWatcher right now —"
    echo "   it'll appear on next login (GNOME needs the AppIndicator extension enabled)."
  fi
fi

echo
echo "Dashboard:  http://$ADDR/"
echo
echo "Next — wire up the statusline (see README, \"Wiring into Claude Code\")."
