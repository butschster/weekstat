# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

One developer on a Claude Code subscription (Pro/Max), sometimes with several
Claude accounts. The live figures (left, today, budget/day) they already see in
the statusline all day. The dashboard they open every couple of days to review
how the week went: the day-by-day breakdown and the charts come first, the
current figures are supporting context.

## Product Purpose

weekstat turns Claude Code's instantaneous "how much of the 7-day quota is
used" into a weekly picture: what is left, what today cost, how much may be
spent per remaining day so the quota lasts until the reset, and whether the
week is on pace. Success: the quota never runs out before the reset by
surprise.

## Positioning

The only numbers Claude Code gives are a used% and a reset time on the
statusline's stdin. weekstat builds the history from them (per-day baselines,
reset-day carry, per-account windows), so the daily budget is stable within a
day and the history survives resets and account switches.

## Operating Context

- Three surfaces read one daemon: the terminal statusline (always visible),
  the GNOME tray ring, and this dashboard at http://127.0.0.1:7457/ (opened in a
  browser tab, local only).
- The dashboard polls `/stats` every few seconds and `/history` less often; it
  updates in place, pauses when hidden, and shows a reconnect banner when the
  daemon is unreachable.
- Claude Code service health from status.claude.com (operational / degraded /
  outage + incident) is part of the picture.

## Capabilities and Constraints

- Data on the page: quota used / left, window start–end and reset countdown,
  pace (on track / slightly over / over budget), budget per day and days left,
  today's spend vs today's allowance (reset-day split), per-day history (30
  days) with window resets, cumulative burn in the current window vs the ideal
  corridor with projection, service health, current account and a switcher
  when more than one account is known, updated time, live/pause.
- One self-contained HTML file embedded in a stdlib-only Go binary: no CDN, no
  external fonts or scripts, works offline.
- Percentages of the subscription quota only — no dollars, no tokens.

## Evidence on Hand

- Live data from the local daemon; `docs/dashboard.png` shows the current
  design. No testimonials, benchmarks or other claims exist and none are to be
  invented.

## Product Principles

- Show how the week went first (days, the burn against an even pace); the live figures are context, the statusline already carries them.
- Numbers are honest: stale, missing or pre-reset data is labelled, never hidden.
- Local and quiet: no network beyond the status page, nothing leaves the machine.

## Brand Commitments

- Dark only: the dashboard ships a single dark theme (confirmed by the owner;
  no light theme, no toggle).

## Accessibility & Inclusion

- Pace is encoded by shape and text as well as colour (colour-blind safe).
- Respects reduced motion; charts carry text alternatives.
