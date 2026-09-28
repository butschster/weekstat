---
version: 1
slug: "dashboard-html"
primary_target: "dashboard.html"
related_targets: []
---

# Surface: weekstat dashboard (dashboard.html)

Mode: Operate. Owner opens it every couple of days to review how the week went; live figures are secondary (the statusline carries them). Dark only.

## Direction contract

THESIS: The week is a flight and the quota its fuel. The page is the night-mode fuel log of an electronic flight bag: planned burn (magenta) against actual, legs (days) logged beneath on the same time axis. It refuses the tile-row-of-big-numbers analytics dashboard.

OWN-WORLD: Blue-black cockpit glass (#0A0F15 ground, #111922 panel), one hairline grid colour, white readouts in tabular monospace digits, system sans for words. Colour is meaning only: magenta = plan (the even-pace line and corridor), green = within plan, amber = caution, red = over; cyan = the selectable thing. Instruments are drawn, not decorated: scales, tick marks, a single ruled log.

STORY: The owner sees at a glance how the week burned against plan, which days were heavy, where this window will end, then reads the current reserve and today's allowance.

FIRST VIEWPORT: Top strip: account + window (departure → arrival) + service + live. Left 2/3: fuel-burn profile of the current window (0–168 h, actual vs magenta plan corridor, projection, day ticks). Beneath on the same day grid: the leg log (one row per day: date, burn bar vs allowance, spent, share, reset marks). Right 1/3: the fuel panel — remaining, budget/day, today burned vs allowance, reset countdown, pace verdict. Below: 30-day consumption history.

FORM: position 3 of 7 on the grounded list (EFB fuel log); seed key f5593a82. Raises: split-flap (fixed-column day rows, state restyles row), dive computer (one shared time axis), Ikeda (colour only where it means something).

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance
