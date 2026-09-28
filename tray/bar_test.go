package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRenderBarWidthAndFill(t *testing.T) {
	cases := []struct {
		frac      float64
		wantFull  int // full blocks expected
		wantEmpty int // light cells expected (partials not counted)
	}{
		{0, 0, 10},
		{0.5, 5, 5},
		{1, 10, 0},
		{2.5, 10, 0},  // overflow clamps to a full bar
		{-0.3, 0, 10}, // negative clamps to empty
	}
	for _, c := range cases {
		got := renderBar(c.frac, 10)
		if n := utf8.RuneCountInString(got); n != 10 {
			t.Errorf("frac %v: width %d, want 10 (%q)", c.frac, n, got)
		}
		if n := strings.Count(got, "█"); n != c.wantFull {
			t.Errorf("frac %v: %d full blocks, want %d (%q)", c.frac, n, c.wantFull, got)
		}
		if n := strings.Count(got, "░"); n != c.wantEmpty {
			t.Errorf("frac %v: %d empty cells, want %d (%q)", c.frac, n, c.wantEmpty, got)
		}
	}
}

func TestRenderBarPartialCell(t *testing.T) {
	// 0.45 of 10 cells = 4 full + 4/8 partial ("▌") + 5 empty.
	got := renderBar(0.45, 10)
	want := "████▌░░░░░"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if renderBar(0.3, 0) != "" {
		t.Error("zero width must render empty")
	}
}

func TestBarLineOverBudgetMarker(t *testing.T) {
	if s := barLine(0.4, "40%"); strings.Contains(s, "⚠") {
		t.Errorf("under budget must not carry the over marker: %q", s)
	}
	if s := barLine(1.3, "130%"); !strings.Contains(s, "⚠") {
		t.Errorf("over budget must carry the over marker: %q", s)
	}
}

func TestFmtCountdown(t *testing.T) {
	cases := []struct {
		hours float64
		want  string
	}{
		{146.5, "146h 30m"},
		{0.5, "0h 30m"},
		{1.99, "1h 59m"},
		{0, "0h 0m"},
		{-2, "0h 0m"}, // stale data past the reset never goes negative
	}
	for _, c := range cases {
		if got := fmtCountdown(c.hours); got != c.want {
			t.Errorf("%v h: got %q, want %q", c.hours, got, c.want)
		}
	}
}

func TestTodayHexThresholds(t *testing.T) {
	cases := []struct {
		spent, budget float64
		want          string
	}{
		{0, 15, colGreen},
		{12.7, 15, colGreen}, // just under 85%
		{12.8, 15, colAmber}, // ≥85% of the allowance
		{15, 15, colAmber},   // exactly at budget is still amber
		{15.1, 15, colRed},   // over budget
		{5, 0, colGrey},      // no budget known yet
	}
	for _, c := range cases {
		if got := todayHex(c.spent, c.budget); got != c.want {
			t.Errorf("spent %v of %v: %s, want %s", c.spent, c.budget, got, c.want)
		}
	}
}

func TestTodaySpentInWindowFallback(t *testing.T) {
	var s stats
	s.Today.SpentPct = 12
	s.Today.SpentInWindowPct = 4
	if got := todaySpentInWindow(&s); got != 4 {
		t.Errorf("got %v, want the in-window figure 4", got)
	}
	s.Today.SpentInWindowPct = 0 // old daemon without the field
	if got := todaySpentInWindow(&s); got != 12 {
		t.Errorf("got %v, want the spent_pct fallback 12", got)
	}
}

func TestRingSpecToday(t *testing.T) {
	var s stats
	s.HasData = true
	s.Quota.Pace = "on_track"
	s.Quota.UsedPct = 16
	s.Today.SpentInWindowPct = 6
	s.Today.BudgetPct = 15

	frac, hex, title, _ := ringSpec(ringToday, &s)
	if frac != 6.0/15.0 {
		t.Errorf("frac = %v, want 0.4", frac)
	}
	if hex != colGreen {
		t.Errorf("hex = %s, want green", hex)
	}
	if title != "9%" {
		t.Errorf("title = %q, want \"9%%\" (left today)", title)
	}
}

func TestRingSpecWeek(t *testing.T) {
	var s stats
	s.HasData = true
	s.Quota.Pace = "over_budget"
	s.Quota.UsedPct = 40
	s.Quota.RemainingPct = 60

	frac, hex, title, _ := ringSpec(ringWeek, &s)
	if frac != 0.4 {
		t.Errorf("frac = %v, want 0.4", frac)
	}
	if hex != colRed {
		t.Errorf("hex = %s, want the over-budget red", hex)
	}
	if title != "40%" {
		t.Errorf("title = %q, want \"40%%\"", title)
	}
}

func TestRingPrefsRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "prefs.json")
	if got := loadRing(path); got != ringWeek {
		t.Errorf("missing file: %s, want the %s default", got, ringWeek)
	}
	saveRing(path, ringToday)
	if got := loadRing(path); got != ringToday {
		t.Errorf("after save: %s, want %s", got, ringToday)
	}
	saveRing(path, "nonsense") // hand-edited garbage falls back to the default
	if got := loadRing(path); got != ringWeek {
		t.Errorf("invalid value: %s, want the %s default", got, ringWeek)
	}
}

func TestServiceLines(t *testing.T) {
	var s stats
	s.Service.Level = levelOutage
	s.Service.Outage = true
	s.Service.Label = "partial outage"
	s.Service.Incident = "Elevated errors for multiple models"
	s.Service.IncidentStatus = "identified"
	s.Service.IncidentSince = "2026-09-03 17:26"
	s.Service.IncidentURL = "https://stspg.io/x"
	if got := serviceLine(&s); got != "Claude Code: ⛔ partial outage" {
		t.Errorf("outage line = %q", got)
	}
	if got := serviceIncidentLine(&s); got != "  ↳ Elevated errors for multiple models" {
		t.Errorf("incident line = %q", got)
	}
	if got := serviceIncidentTip(&s); got != "Elevated errors for multiple models (identified) · since 2026-09-03 17:26 — click to open" {
		t.Errorf("incident tip = %q", got)
	}
	s.Service.Incident = "Elevated error rates on requests to Claude Fable 5.1 and Opus 5 across all regions"
	if got := serviceIncidentLine(&s); len([]rune(got)) != len([]rune("  ↳ "))+maxIncidentRunes || got[len(got)-3:] != "…" {
		t.Errorf("long incident not truncated: %q", got)
	}
	if serviceIncidentURL(&s) != "https://stspg.io/x" || servicePageURL(&s) != "https://status.claude.com" {
		t.Errorf("urls = %q %q", serviceIncidentURL(&s), servicePageURL(&s))
	}

	var ok stats
	ok.Service.Level, ok.Service.Label = levelOK, "operational"
	if got := serviceLine(&ok); got != "Claude Code: ✓ operational" {
		t.Errorf("ok line = %q", got)
	}
	if serviceIncidentLine(&ok) != "" {
		t.Error("no incident expected")
	}

	var old stats // daemon without the check, or status page unreachable
	if got := serviceLine(&old); got != "Claude Code: ? unknown" {
		t.Errorf("unknown line = %q", got)
	}
	old.Service.Error = "dial tcp: timeout"
	if got := serviceLine(&old); got != "Claude Code: ? status page unreachable" {
		t.Errorf("error line = %q", got)
	}
	if servicePageURL(nil) == "" || serviceIncidentURL(nil) == "" {
		t.Error("nil stats must still yield the status page url")
	}
}

func TestOutageIconIsPNG(t *testing.T) {
	b := outageIcon()
	if len(b) < 8 || string(b[1:4]) != "PNG" {
		t.Fatalf("not a PNG (%d bytes)", len(b))
	}
}

func TestAccountLabels(t *testing.T) {
	var s stats
	if accountLine(&s) != "" || accountTag(&s) != "" {
		t.Error("no account block must give no label")
	}
	if err := json.Unmarshal([]byte(`{"account":{"key":"abc","label":"Acme","plan":"max"},"accounts":{"abc":{"label":"Acme"}}}`), &s); err != nil {
		t.Fatal(err)
	}
	if got := accountLine(&s); got != "Account: Acme" {
		t.Errorf("accountLine = %q", got)
	}
	if got := accountTag(&s); got != "Acme" {
		t.Errorf("accountTag with one account = %q, want Acme (always shown)", got)
	}
}
