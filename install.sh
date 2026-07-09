#!/usr/bin/env bash
# weekstat installer — builds the daemon, installs it under ~/.claude/tools/weekstat,
# and registers a systemd --user service that starts on boot.
#
# Usage:  ./install.sh            # build + install + enable service
#         ADDR=127.0.0.1:9000 ./install.sh   # custom dashboard address
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOOL_DIR="$HOME/.claude/tools/weekstat"
ADDR="${ADDR:-127.0.0.1:7457}"
VERSION="$(git -C "$REPO_DIR" describe --tags --always 2>/dev/null || echo dev)"

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

echo
echo "Dashboard:  http://$ADDR/"
echo
echo "Next — wire up the statusline (see README, \"Wiring into Claude Code\")."
