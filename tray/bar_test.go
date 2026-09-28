package main

import (
	"encoding/json"
	"os"
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

func TestPrefsRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "prefs.json")
	if got := loadPrefs(path); got != (prefs{Ring: ringWeek}) {
		t.Errorf("missing file: %+v, want the week ring following the active account", got)
	}
	savePrefs(path, prefs{Ring: ringToday, Account: "abc"})
	if got := loadPrefs(path); got != (prefs{Ring: ringToday, Account: "abc"}) {
		t.Errorf("after save: %+v", got)
	}
	savePrefs(path, prefs{Ring: "nonsense", Account: "abc"}) // hand-edited garbage falls back to the default
	if got := loadPrefs(path); got.Ring != ringWeek || got.Account != "abc" {
		t.Errorf("invalid ring: %+v, want the %s default and the account kept", got, ringWeek)
	}
}

func TestPrefsReadsOldFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.json")
	if err := os.WriteFile(path, []byte(`{"ring":"today"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadPrefs(path); got != (prefs{Ring: ringToday}) {
		t.Errorf("pre-account prefs = %+v, want today ring following the active account", got)
	}
}

func TestAccountChoices(t *testing.T) {
	var s stats
	if err := json.Unmarshal([]byte(`{"accounts":{
		"k2":{"label":"zed@example.com","plan":"pro","active":true},
		"k1":{"label":"amy@example.com","plan":"max"},
		"k0":{"label":"amy@example.com"}}}`), &s); err != nil {
		t.Fatal(err)
	}
	got := accountChoices(&s)
	if len(got) != 3 || got[0].Key != "k0" || got[1].Key != "k1" || got[2].Key != "k2" {
		t.Fatalf("choices = %+v, want by label then key", got)
	}
	if title := choiceTitle(got[2]); title != "zed@example.com · pro  (active)" {
		t.Errorf("title = %q", title)
	}
	if title := choiceTitle(accountChoice{Key: "k9"}); title != "k9" {
		t.Errorf("unlabelled title = %q, want the key", title)
	}
}

func TestStatsURLFor(t *testing.T) {
	base := "http://127.0.0.1:7457/stats"
	if got := statsURLFor(base, ""); got != base {
		t.Errorf("follow active = %q", got)
	}
	if got := statsURLFor(base, "9e40-w128"); got != base+"?account=9e40-w128" {
		t.Errorf("pinned = %q", got)
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
	if accountLine(&s, false) != "" || accountTag(&s) != "" {
		t.Error("no account block must give no label")
	}
	if err := json.Unmarshal([]byte(`{"account":{"key":"abc","label":"Acme","plan":"max"},"accounts":{"abc":{"label":"Acme"}}}`), &s); err != nil {
		t.Fatal(err)
	}
	if got := accountLine(&s, false); got != "Account: Acme" {
		t.Errorf("accountLine = %q", got)
	}
	s.Accounts["def"] = struct {
		Label  string `json:"label"`
		Plan   string `json:"plan"`
		Active bool   `json:"active"`
	}{Label: "Globex"}
	if got := accountLine(&s, true); got != "Account: Acme · pinned" {
		t.Errorf("accountLine pinned = %q", got)
	}
	if got := accountLine(&s, false); got != "Account: Acme · active" {
		t.Errorf("accountLine following = %q", got)
	}
	if got := accountTag(&s); got != "Acme" {
		t.Errorf("accountTag with one account = %q, want Acme (always shown)", got)
	}
}
