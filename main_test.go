package main

import (
	"encoding/json"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newApp() *App {
	return &App{st: newState()}
}

func at(y int, mo time.Month, d, h, min int) time.Time {
	return time.Date(y, mo, d, h, min, 0, 0, time.Local)
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

// ---------------------------------------------------------------------------
// parseResets
// ---------------------------------------------------------------------------

func TestParseResets(t *testing.T) {
	epoch := int64(1787990400) // some fixed moment
	cases := []struct {
		name string
		in   json.RawMessage
		want int64
		ok   bool
	}{
		{"epoch seconds", raw("1787990400"), epoch, true},
		{"epoch milliseconds", raw("1787990400000"), epoch, true},
		{"epoch as string", raw(`"1787990400"`), epoch, true},
		{"rfc3339", raw(`"` + time.Unix(epoch, 0).UTC().Format(time.RFC3339) + `"`), epoch, true},
		{"null", raw("null"), 0, false},
		{"empty", nil, 0, false},
		{"garbage", raw(`"not-a-time"`), 0, false},
		{"zero", raw("0"), 0, false},
	}
	for _, c := range cases {
		got, ok := parseResets(c.in)
		if ok != c.ok {
			t.Errorf("%s: ok = %v, want %v", c.name, ok, c.ok)
			continue
		}
		if ok && got.Unix() != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got.Unix(), c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// spend accounting
// ---------------------------------------------------------------------------

func TestSpentOfClampsNegative(t *testing.T) {
	d := &DayStat{StartUsed: 70, EndUsed: 10}
	if got := windowSpentOf(d); got != 0 {
		t.Errorf("windowSpentOf = %v, want 0", got)
	}
	d.CarrySpent = 8
	if got := spentOf(d); got != 8 {
		t.Errorf("spentOf = %v, want 8 (carry only)", got)
	}
}

func TestSampleNormalDay(t *testing.T) {
	a := newApp()
	reset := at(2026, time.August, 25, 12, 0)

	a.sample(10, reset, at(2026, time.August, 19, 9, 0))
	a.sample(16, reset, at(2026, time.August, 19, 15, 0))

	d := a.st.Days["2026-08-19"]
	if d == nil {
		t.Fatal("day record missing")
	}
	if d.StartUsed != 10 || d.EndUsed != 16 {
		t.Errorf("baseline: start %v end %v, want 10/16", d.StartUsed, d.EndUsed)
	}
	if got := spentOf(d); got != 6 {
		t.Errorf("spent = %v, want 6", got)
	}
}

// The core reset-day bug: the window flips mid-day, used% drops, and without
// re-baselining both today's spend and the daily budget go wrong.
func TestSampleMidDayReset(t *testing.T) {
	a := newApp()
	oldReset := at(2026, time.August, 18, 12, 0)
	newReset := at(2026, time.August, 25, 12, 0)

	a.sample(70, oldReset, at(2026, time.August, 18, 9, 0))
	a.sample(78, oldReset, at(2026, time.August, 18, 11, 55))
	a.sample(2, newReset, at(2026, time.August, 18, 12, 5)) // reset happened
	a.sample(6, newReset, at(2026, time.August, 18, 15, 0))

	d := a.st.Days["2026-08-18"]
	if d == nil {
		t.Fatal("day record missing")
	}
	if d.CarrySpent != 8 {
		t.Errorf("carry = %v, want 8 (78-70 banked from the old window)", d.CarrySpent)
	}
	if d.StartUsed != 2 {
		t.Errorf("re-baselined start = %v, want 2 (first reading of the new window)", d.StartUsed)
	}
	if got := windowSpentOf(d); got != 4 {
		t.Errorf("in-window spend = %v, want 4", got)
	}
	if got := spentOf(d); got != 12 {
		t.Errorf("full-day spend = %v, want 12 (8 banked + 4 new)", got)
	}
	if a.st.WindowEndUnix != newReset.Unix() {
		t.Errorf("window end = %d, want %d", a.st.WindowEndUnix, newReset.Unix())
	}
	if d.WindowEnd != newReset.Unix() {
		t.Errorf("day tagged with window %d, want %d", d.WindowEnd, newReset.Unix())
	}
}

// A double reset in one day (pathological, but the arithmetic must still hold).
func TestSampleTwoResetsSameDay(t *testing.T) {
	a := newApp()
	r1 := at(2026, time.August, 18, 8, 0)
	r2 := at(2026, time.August, 25, 8, 0)
	r3 := at(2026, time.September, 1, 8, 0)

	// Keyed: a second window change within two hours only makes sense for one
	// known account — without a key it would read as another account.
	acct := AccountInfo{Key: "k1"}
	a.sampleAccount(acct, 90, r1, at(2026, time.August, 18, 7, 0))
	a.sampleAccount(acct, 95, r1, at(2026, time.August, 18, 7, 59))
	a.sampleAccount(acct, 1, r2, at(2026, time.August, 18, 8, 5))
	a.sampleAccount(acct, 3, r2, at(2026, time.August, 18, 9, 0))
	a.sampleAccount(acct, 0, r3, at(2026, time.August, 18, 10, 0))
	a.sampleAccount(acct, 2, r3, at(2026, time.August, 18, 11, 0))

	d := a.st.Days["2026-08-18"]
	if d.CarrySpent != 7 { // (95-90) + (3-1)
		t.Errorf("carry = %v, want 7", d.CarrySpent)
	}
	if got := spentOf(d); got != 9 {
		t.Errorf("full-day spend = %v, want 9", got)
	}
}

// Sub-hour jitter in resets_at must not be treated as a new window.
func TestSampleWindowJitterTolerated(t *testing.T) {
	a := newApp()
	reset := at(2026, time.August, 25, 12, 0)

	a.sample(10, reset, at(2026, time.August, 19, 9, 0))
	a.sample(14, reset.Add(90*time.Second), at(2026, time.August, 19, 10, 0))

	d := a.st.Days["2026-08-19"]
	if d.CarrySpent != 0 {
		t.Errorf("carry = %v, want 0 (jitter is not a reset)", d.CarrySpent)
	}
	if a.st.WindowEndUnix != reset.Unix() {
		t.Errorf("window end churned to %d, want stable %d", a.st.WindowEndUnix, reset.Unix())
	}
	if got := spentOf(d); got != 4 {
		t.Errorf("spent = %v, want 4", got)
	}
}

func TestSamplePrunesOldDays(t *testing.T) {
	a := newApp()
	reset := at(2026, time.August, 25, 12, 0)
	a.st.Days["2020-01-01"] = &DayStat{StartUsed: 1, EndUsed: 2}

	a.sample(10, reset, at(2026, time.August, 19, 9, 0))

	if _, ok := a.st.Days["2020-01-01"]; ok {
		t.Error("ancient day survived pruning")
	}
}

// ---------------------------------------------------------------------------
// computeOutput
// ---------------------------------------------------------------------------

func TestComputeOutputNoData(t *testing.T) {
	o := computeOutput(newState(), at(2026, time.August, 19, 12, 0))
	if o.HasData {
		t.Error("HasData = true for empty state")
	}
}

func TestComputeOutputBudgetAndPace(t *testing.T) {
	a := newApp()
	// Window: Tue Aug 18 12:00 → Tue Aug 25 12:00. Now: Wed Aug 19 12:00.
	reset := at(2026, time.August, 25, 12, 0)
	a.sample(10, reset, at(2026, time.August, 19, 9, 0))
	a.sample(16, reset, at(2026, time.August, 19, 12, 0))

	o := computeOutput(a.st, at(2026, time.August, 19, 12, 0))
	if !o.HasData {
		t.Fatal("no data")
	}
	// Days remaining: Wed..Mon before the Tue reset date = Aug 19..25 → 6 days.
	// Budget: quota left at start of today (100-10=90) over 6 days = 15.
	if o.Quota.BudgetPerDayPct != 15 {
		t.Errorf("budget/day = %v, want 15", o.Quota.BudgetPerDayPct)
	}
	if o.Today.SpentPct != 6 || o.Today.SpentInWindowPct != 6 {
		t.Errorf("today spent = %v/%v, want 6/6", o.Today.SpentPct, o.Today.SpentInWindowPct)
	}
	if o.Today.LeftPct != 9 {
		t.Errorf("today left = %v, want 9", o.Today.LeftPct)
	}
	// Elapsed 24h of 168h = 14.3%; used 16% → within +10pp → slightly_over.
	if o.Quota.Pace != "slightly_over" {
		t.Errorf("pace = %q, want slightly_over", o.Quota.Pace)
	}
}

// After a mid-day reset the daily budget must come from the fresh window's
// baseline, not from the pre-reset one.
func TestComputeOutputBudgetAfterMidDayReset(t *testing.T) {
	a := newApp()
	oldReset := at(2026, time.August, 18, 12, 0)
	newReset := at(2026, time.August, 25, 12, 0)

	a.sample(70, oldReset, at(2026, time.August, 18, 9, 0))
	a.sample(78, oldReset, at(2026, time.August, 18, 11, 55))
	a.sample(2, newReset, at(2026, time.August, 18, 12, 5))
	a.sample(6, newReset, at(2026, time.August, 18, 15, 0))

	o := computeOutput(a.st, at(2026, time.August, 18, 15, 0))
	// Days remaining: Tue Aug 18 .. Mon Aug 24 = 7. Quota left at the new
	// baseline: 100-2=98 → 14/day. The buggy version used the stale baseline
	// (100-70=30 → 4.3/day).
	if o.Quota.BudgetPerDayPct != 14 {
		t.Errorf("budget/day = %v, want 14", o.Quota.BudgetPerDayPct)
	}
	if o.Today.SpentPct != 12 {
		t.Errorf("today spent = %v, want 12 (8 banked + 4 new)", o.Today.SpentPct)
	}
	if o.Today.SpentInWindowPct != 4 {
		t.Errorf("today in-window spent = %v, want 4", o.Today.SpentInWindowPct)
	}
	// Overspend is measured against the in-window part only: 14 - 4 = 10.
	if o.Today.LeftPct != 10 {
		t.Errorf("today left = %v, want 10", o.Today.LeftPct)
	}
}

// Morning of the reset day, before the reset: the whole remainder is today's
// budget (daysRemaining clamps to 1).
func TestComputeOutputResetDayMorning(t *testing.T) {
	a := newApp()
	reset := at(2026, time.August, 18, 12, 0)
	a.sample(70, reset, at(2026, time.August, 18, 9, 0))

	o := computeOutput(a.st, at(2026, time.August, 18, 9, 0))
	if o.Quota.BudgetPerDayPct != 30 {
		t.Errorf("budget/day = %v, want 30 (spend the rest before the reset)", o.Quota.BudgetPerDayPct)
	}
}

// Only days of the active window appear in the per-day breakdown; shares sum
// over the window's own consumption.
func TestComputeOutputDaysScopedToWindow(t *testing.T) {
	a := newApp()
	oldReset := at(2026, time.August, 11, 12, 0)
	newReset := at(2026, time.August, 18, 12, 0)

	a.sample(50, oldReset, at(2026, time.August, 15, 10, 0))
	a.sample(60, oldReset, at(2026, time.August, 15, 20, 0))
	a.sample(2, newReset, at(2026, time.August, 18, 13, 0))
	a.sample(8, newReset, at(2026, time.August, 18, 20, 0))

	o := computeOutput(a.st, at(2026, time.August, 18, 20, 0))
	if len(o.Days) != 1 {
		t.Fatalf("days in current window = %d, want 1", len(o.Days))
	}
	if o.Days[0].Date != "2026-08-18" || o.Days[0].SpentPct != 6 {
		t.Errorf("day = %+v, want 2026-08-18 spent 6", o.Days[0])
	}
	if o.Days[0].SharePct != 100 {
		t.Errorf("share = %v, want 100", o.Days[0].SharePct)
	}
}

// ---------------------------------------------------------------------------
// historyOutput
// ---------------------------------------------------------------------------

func TestHistoryOutput(t *testing.T) {
	a := newApp()
	oldReset := at(2026, time.August, 18, 12, 0)
	newReset := at(2026, time.August, 25, 12, 0)

	a.sample(60, oldReset, at(2026, time.August, 17, 10, 0))
	a.sample(70, oldReset, at(2026, time.August, 17, 20, 0))
	a.sample(72, oldReset, at(2026, time.August, 18, 9, 0))
	a.sample(74, oldReset, at(2026, time.August, 18, 11, 0))
	a.sample(2, newReset, at(2026, time.August, 18, 13, 0))
	a.sample(10, newReset, at(2026, time.August, 18, 23, 0))
	a.sample(10, newReset, at(2026, time.August, 19, 9, 0))
	a.sample(16, newReset, at(2026, time.August, 19, 12, 0))

	h := historyOutput(a.st, at(2026, time.August, 19, 12, 0), 30)
	if len(h.Records) != 3 {
		t.Fatalf("records = %d, want 3", len(h.Records))
	}
	byDate := map[string]HistRecord{}
	for _, r := range h.Records {
		byDate[r.Date] = r
	}
	if got := byDate["2026-08-17"].SpentPct; got != 10 {
		t.Errorf("Aug 17 spent = %v, want 10", got)
	}
	// Reset day: 2 from the old window (72→74) + 8 in the new (2→10).
	if got := byDate["2026-08-18"].SpentPct; got != 10 {
		t.Errorf("Aug 18 spent = %v, want 10", got)
	}
	if !byDate["2026-08-18"].IsWindowStart {
		t.Error("Aug 18 not flagged as window start")
	}
	if byDate["2026-08-19"].IsWindowStart {
		t.Error("Aug 19 wrongly flagged as window start")
	}
	if !byDate["2026-08-19"].IsToday {
		t.Error("Aug 19 not flagged as today")
	}
}

func TestHistoryCutoff(t *testing.T) {
	a := newApp()
	reset := at(2026, time.August, 25, 12, 0)
	a.sample(5, reset, at(2026, time.August, 1, 10, 0))
	a.sample(10, reset, at(2026, time.August, 19, 10, 0))

	h := historyOutput(a.st, at(2026, time.August, 19, 12, 0), 7)
	for _, r := range h.Records {
		if r.Date < "2026-08-13" {
			t.Errorf("record %s older than the 7-day cutoff", r.Date)
		}
	}
}

// ---------------------------------------------------------------------------
// small pieces
// ---------------------------------------------------------------------------

func TestSameWindow(t *testing.T) {
	if !sameWindow(1000, 1000+3600) {
		t.Error("1h drift should be the same window")
	}
	if sameWindow(1000, 1000+3601) {
		t.Error("more than 1h apart should be a different window")
	}
	if sameWindow(0, at(2026, time.August, 25, 12, 0).Unix()) {
		t.Error("zero (no window yet) must never match a real window")
	}
}

func TestBuildDaysShares(t *testing.T) {
	days := map[string]*DayStat{
		"2026-08-18": {StartUsed: 0, EndUsed: 6},
		"2026-08-19": {StartUsed: 6, EndUsed: 24},
	}
	out := buildDays(days, "2026-08-19")
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
	if out[0].SharePct != 25 || out[1].SharePct != 75 {
		t.Errorf("shares = %v/%v, want 25/75", out[0].SharePct, out[1].SharePct)
	}
	if !out[1].IsToday {
		t.Error("today flag lost")
	}
}
