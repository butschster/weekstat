---
name: weekstat
description: Night-mode fuel log for a Claude Code weekly quota. Planned burn against actual, one day per leg.
colors:
  cockpit-ground: "#0A0F15"
  panel-glass: "#0F1720"
  panel-glass-raised: "#131D28"
  hairline-rule: "#1F2B37"
  scale-grid: "#1A2530"
  day-rule: "#223040"
  hover-rule: "#2E3E4E"
  readout-white: "#E6EDF3"
  label-grey: "#A9B8C6"
  dim-grey: "#7C8EA0"
  column-steel: "#5E7185"
  weekend-steel: "#3E5063"
  plan-magenta: "#FF4FD8"
  within-green: "#34E0A1"
  caution-amber: "#FFB020"
  over-red: "#FF5A4F"
  select-cyan: "#3FD0FF"
typography:
  display:
    fontFamily: "system-ui, -apple-system, 'Segoe UI', Roboto, 'Helvetica Neue', sans-serif"
    fontSize: "19px"
    fontWeight: 700
    lineHeight: 1.15
    letterSpacing: "0.02em"
  readout:
    fontFamily: "ui-monospace, 'SF Mono', 'JetBrains Mono', 'Cascadia Mono', Menlo, Consolas, monospace"
    fontSize: "20px"
    fontWeight: 500
    letterSpacing: "-0.01em"
    fontFeature: "tnum, lnum"
  readout-sm:
    fontFamily: "ui-monospace, 'SF Mono', 'JetBrains Mono', 'Cascadia Mono', Menlo, Consolas, monospace"
    fontSize: "15px"
    fontWeight: 500
    fontFeature: "tnum, lnum"
  title:
    fontFamily: "system-ui, -apple-system, 'Segoe UI', Roboto, 'Helvetica Neue', sans-serif"
    fontSize: "16px"
    fontWeight: 600
    lineHeight: 1.5
    letterSpacing: "0.005em"
  body:
    fontFamily: "system-ui, -apple-system, 'Segoe UI', Roboto, 'Helvetica Neue', sans-serif"
    fontSize: "14px"
    fontWeight: 400
    lineHeight: 1.5
  label:
    fontFamily: "system-ui, -apple-system, 'Segoe UI', Roboto, 'Helvetica Neue', sans-serif"
    fontSize: "12px"
    fontWeight: 400
    lineHeight: 1.5
  axis:
    fontFamily: "ui-monospace, 'SF Mono', 'JetBrains Mono', 'Cascadia Mono', Menlo, Consolas, monospace"
    fontSize: "11px"
    fontWeight: 400
rounded:
  bar: "1px"
  focus: "4px"
  panel: "6px"
spacing:
  xs: "6px"
  sm: "10px"
  md: "16px"
  lg: "20px"
  page-x: "24px"
components:
  panel:
    backgroundColor: "{colors.panel-glass}"
    rounded: "{rounded.panel}"
    padding: "14px 16px 16px"
  readout-row:
    textColor: "{colors.readout-white}"
    typography: "{typography.readout}"
    padding: "11px 0"
  gauge:
    backgroundColor: "{colors.scale-grid}"
    rounded: "{rounded.bar}"
    height: "8px"
  leg-bar:
    backgroundColor: "{colors.scale-grid}"
    rounded: "{rounded.bar}"
    height: "5px"
  live-toggle:
    textColor: "{colors.label-grey}"
    rounded: "{rounded.panel}"
    padding: "4px 9px"
  live-toggle-hover:
    textColor: "{colors.readout-white}"
  account-select:
    backgroundColor: "{colors.panel-glass-raised}"
    textColor: "{colors.readout-white}"
    rounded: "{rounded.panel}"
    padding: "4px 8px"
  annunciator:
    backgroundColor: "{colors.panel-glass}"
    rounded: "{rounded.panel}"
    padding: "9px 12px"
  tooltip:
    backgroundColor: "{colors.panel-glass-raised}"
    textColor: "{colors.readout-white}"
    rounded: "{rounded.panel}"
    padding: "5px 8px"
---

# Design System: weekstat

## Overview

**Creative North Star: "The Night Fuel Log"**

The week is a flight and the quota is its fuel. The dashboard reads like the night-mode fuel page of an electronic flight bag: blue-black cockpit glass, a planned burn drawn in magenta, the actual burn in white on top of it, and the legs (days) logged directly beneath on the same time axis. Instruments are drawn, not decorated: scales, tick marks, hairline rules, one ruled log. Nothing is ornament; every stroke is a reading or the scale it is read against.

Density is an operator's: 14px body, 12px labels, tabular monospace digits for every figure, panels separated by 20px and ruled internally by 1px hairlines. Colour is scarce and semantic. On a quiet week the page is almost entirely grey-on-black with one magenta plan line and a little green; red appears only when something is actually over. The page is dark only, by owner decision; there is no light theme and no toggle.

It refuses the tile-row-of-big-numbers analytics dashboard. The largest type on the page is a 20px readout, and the charts, not the figures, lead.

**Key Characteristics:**
- Blue-black cockpit glass, flat panels, hairline rules; no gradients, no glows.
- Colour is meaning only: magenta plan, green/amber/red state, cyan today and selectable, white actual.
- Every number is monospace with tabular lining digits; every word is system sans.
- One shared time axis: the day log sits exactly under its span of the burn curve.
- State is carried by shape and text as well as colour (check circle, triangle, octagon; "On track", "Over budget").

## Colors

A near-black blue ground with a cool grey text ladder, and five saturated signal colours that each mean exactly one thing.

### Primary
- **Plan Magenta** (plan-magenta): the plan and only the plan. The even-pace line (1.5px) and its corridor (11-14% tint), the even-day tick on every leg bar and on the Remaining gauge, the dashed even-day rule on the 30-day chart, weekly reset rules, and their `plan` / `reset` / `even` labels.

### Secondary
- **Within Green** (within-green): within plan. On-track verdict, fill of leg bars and gauges under the caution threshold, live dot, operational service dot.
- **Caution Amber** (caution-amber): caution. Days over 1.5 even days, today's gauge past 85% of today's allowance, "Slightly over" verdict, degraded service.
- **Over Red** (over-red): over. Days over 2 even days, today's gauge over its allowance, "Over budget" and "API down" verdicts, the over-budget annunciator, the "runs out" shaded region and label, outage service.

### Tertiary
- **Select Cyan** (select-cyan): today and the selectable. Today's leg (6% tint plus a 2px top rule), today's column and axis label, focus rings, links, text selection, the value-change flash.

### Neutral
- **Cockpit Ground** (cockpit-ground): page and scrollbar track; also the knockout inside the filled "API down" glyph.
- **Panel Glass** (panel-glass) and **Raised Glass** (panel-glass-raised): panel fill; raised glass for the account select and tooltips.
- **Hairline Rule** (hairline-rule): every panel border, strip underline and readout divider.
- **Scale Grid** (scale-grid): chart gridlines and the empty track of bars and gauges.
- **Day Rule** (day-rule): day boundaries in the burn profile and between leg cells.
- **Hover Rule** (hover-rule): border of hovered controls and tooltips.
- **Readout White** (readout-white): body text, every value, and the actual-burn line.
- **Label Grey** (label-grey): labels, secondary copy, legend text.
- **Dim Grey** (dim-grey): units, hints, axis numbers, days ahead, footer.
- **Column Steel** (column-steel) and **Weekend Steel** (weekend-steel): 30-day columns within plan on weekdays and weekends.

### Named Rules

**The Colour Is Meaning Rule.** A saturated colour on this page is a statement: magenta says "plan", green/amber/red say "within / caution / over", cyan says "today" or "you can act on this", white says "actual". Anything without meaning is drawn in the grey ladder.

**The Magenta Is Plan Rule.** Magenta never marks actual use, state or emphasis. If it is not the even pace, the plan corridor, an even day or a reset, it is not magenta.

**The Fixed Thresholds Rule.** An even day is 100/7 % of the quota. A day is amber past 1.5 even days and red past 2 even days; the plan band is plus or minus half an even day, fixed for the window. Thresholds do not scale with the data.

## Typography

**Display Font:** system-ui (with -apple-system, Segoe UI, Roboto, Helvetica Neue, sans-serif)
**Body Font:** the same system sans
**Label/Mono Font:** ui-monospace (with SF Mono, JetBrains Mono, Cascadia Mono, Menlo, Consolas, monospace)

**Character:** Words in the platform's own sans, numbers in the platform's monospace with tabular lining digits, so columns of figures align like a printed log. No webfonts: the page is embedded in a Go binary and works offline.

### Hierarchy
- **Display** (700, 19px, 1.15, +0.02em): the pace verdict word only ("On track", "Over budget", "API down"), in the state colour.
- **Readout** (mono 500, 20px; 18px under 640px): the headline figure of a fuel-panel row (Remaining). **Readout-sm** (mono 500, 15px) for the other rows; leg values at mono 500 13px.
- **Title** (600, 16px): panel headings.
- **Body** (400, 14px, 1.5): verdict clause and annunciator text at 13px.
- **Label** (400, 12-12.5px): row keys, hints, legends, captions, strip text. Units sit after the number at 0.78em in dim grey.
- **Axis** (mono 400, 11px): chart scale numbers; day labels on the time axis are sans 12px, today's in cyan 600.

### Named Rules

**The Mono Digits Rule.** Every figure (percent, date, time, count) is set in the mono stack with `tabular-nums lining-nums`. A number in sans is a bug.

**The Small Ceiling Rule.** Nothing on the page is larger than 20px. Hierarchy comes from weight, colour and position, not size (scale about 1.15-1.2).

## Layout

A centred column (max 1240px, 18px 24px 40px padding). Top: the flight strip (mark, account, service chip, updated time, LIVE toggle, then a full-width route line WINDOW start -> end, resets in), underlined by a hairline. Annunciators stack under the strip. Then the deck: a two-column grid, burn profile left (fluid) and the fuel panel right (fixed 320px), 20px gap, top-aligned. Below, full width, the 30-day log; then a quiet footer.

The burn profile and the leg log share one horizontal mapping (hours 0-168 into the window). Leg cells are absolutely positioned by their share of the window and aligned to the chart's plot margins, so each day sits exactly under its span of the curve.

Rhythm: 6-10px inside rows, 16px panel padding, 20px between panels. Under 980px the deck becomes one column with the fuel panel second. Under 640px the page padding drops to 12px 14px, the strip wraps to full-width rows, and the leg log turns from positioned cells into a ruled table (date 78px | bar | value 58px | share 64px), one row per day.

## Elevation & Depth

Flat. Depth is tonal: ground, panel glass, raised glass, separated by 1px hairlines. The only shadow is the hover tooltip's soft lift; state emphasis uses tints (6-11% colour-mix into the panel), never shadow.

### Shadow Vocabulary
- **Tooltip lift** (`box-shadow: 0 3px 8px rgba(0,0,0,.4)`): chart hover tooltips only.

### Named Rules

**The Glass Not Paper Rule.** Surfaces never float. If something needs to stand out, give it a hairline in its state colour and a faint tint, as the annunciators do.

## Shapes

Gently squared: 6px radius on panels, controls, annunciators and tooltips; 1px on bars, gauges and chart columns so they read as instrument fills, not pills; circles only for status dots and the live-head marker. Borders are 1px hairlines. Plan marks are 1.5px vertical ticks that overshoot their bar by 3-4px each side. Dashes carry meaning: dashed white-to-state line is a projection, dashed magenta is a plan reference (even day, reset), a dashed empty track is a day not yet flown.

## Components

### Panel
Flat glass box: panel-glass fill, 1px hairline border, 6px radius. Header row: 16px/600 title left, 12px dim hint right, 14px 16px 4px padding.

### Flight strip
- **Style:** a single header row, hairline underline, no fill. Mark (20px drawn gauge + 15px/650 wordmark), account name in mono or a select when two or more accounts exist, service chip, "updated HH:MM", LIVE toggle.
- **Route line:** dim grey 12px WINDOW label, mono start and end times with a drawn arrow, "resets in 72h 42m".

### Buttons (LIVE toggle)
- **Shape:** 6px radius, 1px hairline border, transparent fill, 4px 9px padding.
- **Label:** 600 12px sans, uppercase, +0.06em, label grey with a 7px green dot; paused state shows PAUSED with a dim dot.
- **Hover / Focus:** border to hover-rule, text to readout white; focus is a 2px cyan outline offset 2px.

### Inputs / Fields (account select)
- **Style:** raised glass fill, 1px hairline border, 6px radius, 12px sans, 4px 8px padding. Hover lifts the border to hover-rule; focus is the cyan ring.

### Chips (service chip)
- **Style:** text link without underline: 7px dot plus "Claude Code: status" in 12px label grey. Dot green / amber / red by level; the whole chip turns red on outage.

### Annunciators
- **Style:** hairline panel strip, 6px radius, 9px 12px padding, 13px text, a 16px drawn glyph, bold lead phrase then a grey sub-clause.
- **States:** caution (amber at 45% into the border, 8% tint) and warn (red at 50%, 9% tint); the reconnect banner stays neutral with a spinner. Outage and over-budget annunciators can show together; one never hides the other.

### Fuel panel readout row
- **Style:** two-column grid (key left, value right) with a full-width meta line under it; rows ruled by hairlines, 11px vertical padding.
- **Gauge:** 8px track in scale-grid, 1px radius, fill in the state colour animated with scaleX over .25s cubic-bezier(.22,1,.36,1); a magenta tick marks where an even pace would be now.
- **Verdict:** 34px drawn glyph (check circle / triangle / octagon; filled bar-circle for API down) beside the 19px verdict word and a 13px clause.

### Leg log (signature)
One cell per calendar day of the window, positioned under its span of the burn curve. Each cell: day label ("Thu **24** Sep", today reads "Today" in cyan), a 5px burn bar scaled to two even days with the magenta even-day tick at its midpoint, the mono value in %, and its share of the window ("49% of wk"). Fill is green, amber past 1.5 even days, red past 2. Days ahead are dim with a dashed empty track and an em dash; today carries a 6% cyan tint and a 2px cyan top rule. Narrow cells drop the month and share; tiny cells drop the value. Under 640px it becomes a ruled table.

### Burn profile chart (signature)
0-100% by 0-168h. Grid at 25% steps, day rules at local midnight, day labels under their span. Magenta corridor plus 1.5px magenta plan line labelled `plan`. Actual burn is a 2.2px white line ending in a 4.5px white head ringed in panel colour; any stretch above the corridor is overdrawn in the pace colour. Projection is a 5-5 dashed line in the pace colour, labelled "N% at reset"; if it crosses 100%, the rest of the window is tinted red and labelled "runs out".

### 30-day log (signature)
One column per day (max 18px wide, 1px radius): column steel on weekdays, weekend steel on weekends, amber and red by the day thresholds, today cyan. A 6-4 dashed magenta even-day rule sits on top, labelled `even 14.3%`; 3-3 dashed magenta rules mark weekly resets. Hover shows a dashed crosshair and tooltip.

## Do's and Don'ts

### Do:
- **Do** draw every plan reference (even pace, corridor, even day, reset) in plan magenta, and nothing else in it.
- **Do** set every figure in the mono stack with tabular lining digits, units in dim grey at 0.78em.
- **Do** pair every state colour with a shape or word (glyph, verdict text, "runs out", "Over budget").
- **Do** keep the day thresholds fixed: amber past 1.5 even days, red past 2; plan band ± half an even day.
- **Do** keep the burn profile and the day log on one shared time axis.
- **Do** label stale, partial and pre-reset values in text rather than hiding them; show partial-day values.
- **Do** give every chart a title, description and a screen-reader data table, and drop animation under reduced motion.

### Don't:
- **Don't** add a light theme or a theme toggle.
- **Don't** load webfonts, CDNs or external scripts; the page must work offline from the binary.
- **Don't** build rows of big-number tiles; no figure exceeds 20px.
- **Don't** use colour for decoration, gradients, glows or drop shadows on surfaces.
- **Don't** mark state with a coloured side stripe on cards or rows.
- **Don't** let one alarm hide another: an outage annunciator never suppresses the over-budget one.
