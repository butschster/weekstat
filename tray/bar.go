// Pure presentation logic for the tray: unicode progress bars, the ring-mode
// decision (what the panel icon shows), and the persisted preference. Kept
// free of systray calls so it's unit-testable.
package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
)

const (
	colGreen = "#009E73" // Okabe-Ito, colorblind-safe
	colAmber = "#E69F00"
	colRed   = "#D55E00"
	colGrey  = "#8A8F99"

	barWidth = 14
)

const (
	ringWeek  = "week"
	ringToday = "today"
)

// service levels published by the daemon (status.go)
const (
	levelOK       = "ok"
	levelDegraded = "degraded"
	levelOutage   = "outage"
)

// outageTitle is the panel text shown instead of used% during an outage.
const outageTitle = "API ✗"

// serviceLine is the "Claude Code: partial outage" menu row.
func serviceLine(s *stats) string {
	sv := &s.Service
	label := sv.Label
	if label == "" {
		label = "unknown"
	}
	switch sv.Level {
	case levelOK:
		return "Claude Code: ✓ " + label
	case levelDegraded:
		return "Claude Code: ▲ " + label
	case levelOutage:
		return "Claude Code: ⛔ " + label
	}
	if sv.Error != "" {
		return "Claude Code: ? status page unreachable"
	}
	return "Claude Code: ? " + label
}

// maxIncidentRunes caps the incident row so a long title doesn't stretch the
// whole dropdown; the full text goes to the row's tooltip.
const maxIncidentRunes = 44

// truncate cuts s to at most n runes, ending with an ellipsis when it did.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// serviceIncidentLine is the incident row under it; empty when no incident.
func serviceIncidentLine(s *stats) string {
	if s.Service.Incident == "" {
		return ""
	}
	return "  ↳ " + truncate(s.Service.Incident, maxIncidentRunes)
}

// serviceIncidentTip is the untruncated incident detail for the row's tooltip.
func serviceIncidentTip(s *stats) string {
	sv := &s.Service
	tip := sv.Incident
	if sv.IncidentStatus != "" {
		tip += " (" + sv.IncidentStatus + ")"
	}
	if sv.IncidentSince != "" {
		tip += " · since " + sv.IncidentSince
	}
	return tip + " — click to open"
}

// serviceTip is the panel tooltip during an outage.
func serviceTip(s *stats) string {
	tip := "weekstat — " + serviceLine(s) + " — usage hidden until the API is back"
	if s.Service.Incident != "" {
		tip += " · " + s.Service.Incident
	}
	return tip
}

func servicePageURL(s *stats) string {
	if s != nil && s.Service.PageURL != "" {
		return s.Service.PageURL
	}
	return "https://status.claude.com"
}

func serviceIncidentURL(s *stats) string {
	if s != nil && s.Service.IncidentURL != "" {
		return s.Service.IncidentURL
	}
	return servicePageURL(s)
}

// partial cells, 1/8 … 7/8 of a block, left-aligned fills.
var eighths = []rune{0, '▏', '▎', '▍', '▌', '▋', '▊', '▉'}

// renderBar draws a `width`-cell unicode progress bar filled to frac (clamped
// to [0,1]) with eighth-block resolution.
func renderBar(frac float64, width int) string {
	if width <= 0 {
		return ""
	}
	cells := clamp01(frac) * float64(width)
	full := int(cells)
	e := int(math.Round((cells - float64(full)) * 8))
	if e == 8 {
		full, e = full+1, 0
	}
	if full >= width {
		full, e = width, 0
	}
	b := make([]rune, 0, width)
	for i := 0; i < full; i++ {
		b = append(b, '█')
	}
	if e > 0 {
		b = append(b, eighths[e])
	}
	for len(b) < width {
		b = append(b, '░')
	}
	return string(b)
}

// barLine renders "▕████░░░▏ <suffix>", flagging over-budget past 100%.
func barLine(frac float64, suffix string) string {
	s := "▕" + renderBar(frac, barWidth) + "▏ " + suffix
	if frac > 1 {
		s += "  ⚠ over"
	}
	return s
}

// fmtCountdown renders fractional hours as "146h 30m" (clamped at zero), so
// the last hour before a reset reads "0h 30m" instead of a bare "0 h".
func fmtCountdown(hours float64) string {
	m := int(math.Round(hours * 60))
	if m < 0 {
		m = 0
	}
	return fmt.Sprintf("%dh %dm", m/60, m%60)
}

// todaySpentInWindow is today's spend counted against the daily budget. Falls
// back to the full-day figure when talking to a daemon that predates the
// spent_in_window_pct field.
func todaySpentInWindow(s *stats) float64 {
	if s.Today.SpentInWindowPct > 0 || s.Today.SpentPct == 0 {
		return s.Today.SpentInWindowPct
	}
	return s.Today.SpentPct
}

// todayHex colors the day gauge by how much of today's allowance is gone.
func todayHex(spent, budget float64) string {
	switch {
	case budget <= 0:
		return colGrey
	case spent > budget:
		return colRed
	case spent >= 0.85*budget:
		return colAmber
	default:
		return colGreen
	}
}

// ringSpec decides what the panel icon shows for the chosen mode: the arc's
// fill fraction, its color, the text next to the icon, and the tooltip.
func ringSpec(mode string, s *stats) (frac float64, hex, title, tip string) {
	_, _, label := paceMeta(s.Quota.Pace)
	if mode == ringToday {
		spent, budget := todaySpentInWindow(s), s.Today.BudgetPct
		if budget > 0 {
			frac = spent / budget
		}
		left := budget - spent
		return frac, todayHex(spent, budget),
			fmt.Sprintf("%.0f%%", left),
			fmt.Sprintf("weekstat — today %.1f%% of %.1f%% burned · %.1f%% left (week: %s, %.0f%% used)",
				spent, budget, left, label, s.Quota.UsedPct)
	}
	hex, _, _ = paceMeta(s.Quota.Pace)
	return s.Quota.UsedPct / 100, hex,
		fmt.Sprintf("%.0f%%", s.Quota.UsedPct),
		fmt.Sprintf("weekstat — %s · %.0f%% used, %.0f%% left", label, s.Quota.UsedPct, s.Quota.RemainingPct)
}

// ---------------------------------------------------------------------------
// persisted preference: which gauge the ring shows
// ---------------------------------------------------------------------------

type prefs struct {
	Ring string `json:"ring"` // "week" | "today"
}

func defaultPrefsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", ".weekstat-tray.json")
}

func loadRing(path string) string {
	var p prefs
	if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &p) == nil {
		if p.Ring == ringToday || p.Ring == ringWeek {
			return p.Ring
		}
	}
	return ringWeek
}

func saveRing(path, ring string) {
	b, _ := json.Marshal(prefs{Ring: ring})
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, b, 0o644)
}
