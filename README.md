# weekstat

A tracker for the **Claude Code weekly quota** (the rolling 7-day
`rate_limits.seven_day`) with a live dashboard. A tiny Go daemon (stdlib only,
zero dependencies) that turns the instantaneous "how much quota is used" into a
useful picture of the week: how much you've spent today, how much you may spend
per day to make it last until the reset, and a per-day breakdown.

```
 wk: ██░░░░░░░░ left 77% · today 4% · budget 15.0%/day for 5.1d · on track ✓
```

- **left** — how much of the weekly quota remains;
- **today** — how much was burned during the current day;
- **budget X%/day** — how much you may spend per day to make the quota last until
  reset (recomputed live: overspending today automatically shrinks the budget for
  the remaining days);
- **pace** — on track / slightly over / over budget.

Plus an HTTP dashboard you can drop in on: **http://127.0.0.1:7457/**

![weekstat dashboard](docs/dashboard.png)

---

## How it works

```
Claude Code ──stdin(JSON)──▶ statusline script
                                 │  ├─▶ draws the terminal line
                                 │  └─▶ dumps stdin to ~/.claude/.statusline-input.json
                                 ▼
                          weekstat (daemon)  ── watches the file, every 5s
                                 │
                                 ├─▶ ~/.claude/week-stats.json  ◀── statusline reads "today"
                                 └─▶ HTTP :7457  (dashboard + /stats + /history + /healthz)
```

Claude Code only exposes `rate_limits.seven_day.used_percentage` and `resets_at`
on the statusline's stdin — ephemeral data on every render. The daemon builds
history from it: it records used% at the start of each day and computes deltas,
tags every day with the window it belongs to, and keeps a rolling multi-window
daily history that feeds `/history` and the dashboard charts.

> Works only on subscription plans (Pro/Max) where Claude Code sends
> `rate_limits`. On a pure API plan that field is absent.

---

## Install

```bash
git clone https://github.com/butschster/weekstat.git
cd weekstat
./install.sh
```

`install.sh` builds the binary into `~/.claude/tools/weekstat/weekstat`, installs
a systemd `--user` service and starts it (boot autostart via linger).

Check:

```bash
systemctl --user status weekstat
curl -s http://127.0.0.1:7457/healthz     # -> ok
```

Prebuilt binaries are also on the [Releases](https://github.com/butschster/weekstat/releases) page.

---

## Wiring into Claude Code

Two steps: point the statusline at a script and add two integration lines to it.

### 1. Set statusLine in `~/.claude/settings.json`

```json
{
  "statusLine": {
    "type": "command",
    "command": "/home/USER/.claude/statusline-command.sh"
  }
}
```

A ready-to-use script lives in [`examples/statusline-command.sh`](examples/statusline-command.sh)
(it draws the directory, git branch, context bar and the weekly quota line). You
can copy it as-is:

```bash
cp examples/statusline-command.sh ~/.claude/statusline-command.sh
chmod +x ~/.claude/statusline-command.sh
```

### 2. Two integration points (if grafting into your own statusline)

**a) Feed stdin to the daemon** — right after reading the input:

```bash
input=$(cat)

_si="$HOME/.claude/.statusline-input.json"
printf '%s' "$input" > "$_si.tmp" && mv -f "$_si.tmp" "$_si"
```

**b) Show "today" from the daemon's output** — where you render the line:

```bash
today=$(jq -r '.today.spent_pct // empty' "$HOME/.claude/week-stats.json" 2>/dev/null)
[ -n "$today" ] && printf ' · today %.0f%%' "$today"
```

Everything else (left%, budget/day, pace) the statusline can compute itself
directly from `rate_limits.seven_day` on stdin — the daemon isn't required for
that; it's needed for "today", the per-day history and the dashboard. The example
in `examples/` does both.

---

## Dashboard & API

| URL | Returns |
|-----|---------|
| `GET /` | Live self-contained dashboard: hero verdict, evidence bar, cumulative-burn chart (vs. ideal corridor) and a 30-day consumption chart. Polls JSON and updates in place — no page reload. |
| `GET /stats` | Current snapshot as JSON (also written to `week-stats.json`) |
| `GET /history?days=N` | Daily time-series that feeds the charts (default 30, max 365) |
| `GET /healthz` | `ok` |

Example `/stats`:

```json
{
  "version": "v1.1.0",
  "window":  { "start": "2026-07-07 12:00", "end": "2026-07-14 12:00", "resets_in_hours": 123.5, "elapsed_pct": 26.5 },
  "quota":   { "used_pct": 23, "remaining_pct": 77, "budget_per_day_pct": 15, "days_left": 5.1, "pace": "on_track" },
  "today":   { "date": "2026-07-09", "start_used_pct": 23, "current_used_pct": 27, "spent_pct": 4 },
  "days":    [ { "date": "2026-07-09", "spent_pct": 4, "share_pct": 100, "is_today": true } ]
}
```

---

## Pacing logic

- `left% = 100 − used%`
- `days_left = (resets_at − now) / 24h` (floored at 0.25)
- **`budget/day = left% / days_left`** — recomputed on every update, so overspending
  on one day automatically lowers the budget for the remaining days (and
  underspending raises it).
- **`today = used% − used%_at_day_start`** (the daemon keeps the baseline of the
  day's first reading).
- **pace** compares used% against the fraction of the window already elapsed:
  `on track` ≤ elapsed%, `slightly over` ≤ +10pp, otherwise `over budget`.

No dollars — everything is a percentage of the subscription quota.

---

## Configuration

Daemon flags (defaults shown):

```
--input     ~/.claude/.statusline-input.json   file to watch (stdin snapshot)
--out       ~/.claude/week-stats.json          where to write the stats
--state     ~/.claude/.weekstat-state.json     resume state file
--addr      127.0.0.1:7457                      HTTP dashboard address
--interval  5s                                   poll interval
```

Change the dashboard address at install time: `ADDR=127.0.0.1:9000 ./install.sh`.

---

## Managing the service

```bash
systemctl --user status weekstat
systemctl --user restart weekstat
systemctl --user stop weekstat
journalctl --user -u weekstat -f     # logs

# rebuild after code changes:
cd ~/.claude/tools/weekstat && go build -o weekstat . && systemctl --user restart weekstat
```

---

## Releases

GitHub Actions CI:

- **ci.yml** — `go vet` + `go build` on every push/PR;
- **release.yml** — on a `v*` tag, builds binaries (linux/darwin × amd64/arm64),
  writes `checksums.txt` and publishes a GitHub Release.

Cutting a new version:

```bash
git tag v1.0.0
git push origin v1.0.0
```

---

## License

MIT — see [LICENSE](LICENSE).
