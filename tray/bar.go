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
