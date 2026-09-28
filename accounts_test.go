package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var (
	acctA = AccountInfo{Key: "aaaaaaaaaaaa", Label: "Acme", Plan: "default_claude_max_20x"}
	acctB = AccountInfo{Key: "bbbbbbbbbbbb", Label: "Globex", Plan: "claude_pro"}
)

func todaySpent(t *testing.T, a *App, key, day string) *DayStat {
	t.Helper()
	acc, ok := a.st.Accounts[key]
	if !ok {
		t.Fatalf("account %q missing; have %v", key, a.st.sortedKeys())
	}
	d, ok := acc.Days[day]
	if !ok {
		t.Fatalf("account %q has no day %s", key, day)
	}
	return d
}

// A→B→A: switching accounts must not look like a window reset, and today's
// spend must not mix the two accounts.
func TestAccountSwitchIsNotAReset(t *testing.T) {
	a := newApp()
	endA := at(2026, time.August, 25, 12, 0)
	endB := at(2026, time.August, 22, 18, 0)

	a.sampleAccount(acctA, 10, endA, at(2026, time.August, 19, 9, 0))
	a.sampleAccount(acctA, 14, endA, at(2026, time.August, 19, 10, 0))
	a.sampleAccount(acctB, 50, endB, at(2026, time.August, 19, 11, 0))
	a.sampleAccount(acctB, 55, endB, at(2026, time.August, 19, 12, 0))
	a.sampleAccount(acctA, 16, endA, at(2026, time.August, 19, 13, 0))

	dA := todaySpent(t, a, acctA.Key, "2026-08-19")
	if dA.CarrySpent != 0 || spentOf(dA) != 6 {
		t.Errorf("A: carry=%v spent=%v, want 0 and 6", dA.CarrySpent, spentOf(dA))
	}
	dB := todaySpent(t, a, acctB.Key, "2026-08-19")
	if spentOf(dB) != 5 {
		t.Errorf("B: spent=%v, want 5", spentOf(dB))
	}
	if a.st.Accounts[acctA.Key].WindowEndUnix != endA.Unix() {
		t.Error("A's window churned")
	}
	// the mirror (and the output) follow the active account
	if a.st.Active != acctA.Key || a.st.WindowEndUnix != endA.Unix() || a.st.UsedPct != 16 {
		t.Errorf("mirror = %s/%d/%v, want A's", a.st.Active, a.st.WindowEndUnix, a.st.UsedPct)
	}
	o := computeOutput(a.st, at(2026, time.August, 19, 13, 0))
	if o.Today.SpentPct != 6 || o.Account == nil || o.Account.Key != acctA.Key {
		t.Errorf("output today=%v account=%+v, want 6 for A", o.Today.SpentPct, o.Account)
	}
	h := historyOutput(a.st, at(2026, time.August, 19, 13, 0), 30)
	for _, r := range h.Records {
		if r.IsWindowStart {
			t.Errorf("history marks a reset on %s", r.Date)
		}
	}
}

// Two accounts whose windows reset within the same hour are still separate
// when the statusline sends keys.
func TestAccountsWithCoincidentWindows(t *testing.T) {
	a := newApp()
	end := at(2026, time.August, 25, 12, 0)

	a.sampleAccount(acctA, 10, end, at(2026, time.August, 19, 9, 0))
	a.sampleAccount(acctB, 60, end.Add(20*time.Minute), at(2026, time.August, 19, 9, 5))
	a.sampleAccount(acctA, 14, end, at(2026, time.August, 19, 10, 0))
	a.sampleAccount(acctB, 62, end.Add(20*time.Minute), at(2026, time.August, 19, 10, 5))

	if got := spentOf(todaySpent(t, a, acctA.Key, "2026-08-19")); got != 4 {
		t.Errorf("A spent = %v, want 4", got)
	}
	if got := spentOf(todaySpent(t, a, acctB.Key, "2026-08-19")); got != 2 {
		t.Errorf("B spent = %v, want 2", got)
	}
}

// Two sessions on different accounts render alternately; neither account's
// state flaps (no carry, no re-baselining, spend exact).
func TestConcurrentSessionsDoNotFlap(t *testing.T) {
	a := newApp()
	endA := at(2026, time.August, 25, 12, 0)
	endB := at(2026, time.August, 21, 7, 0)
	for i := 0; i < 6; i++ {
		now := at(2026, time.August, 19, 9, 0).Add(time.Duration(i) * 5 * time.Second)
		a.sampleAccount(acctA, float64(20+i), endA, now)
		a.sampleAccount(acctB, float64(40+2*i), endB, now.Add(time.Second))
	}
	dA := todaySpent(t, a, acctA.Key, "2026-08-19")
	dB := todaySpent(t, a, acctB.Key, "2026-08-19")
	if dA.CarrySpent != 0 || dB.CarrySpent != 0 {
		t.Errorf("carry A=%v B=%v, want 0", dA.CarrySpent, dB.CarrySpent)
	}
	if spentOf(dA) != 5 || spentOf(dB) != 10 {
		t.Errorf("spent A=%v B=%v, want 5 and 10", spentOf(dA), spentOf(dB))
	}
	if len(a.st.Accounts) != 2 {
		t.Errorf("accounts = %v, want 2", a.st.sortedKeys())
	}
}

// A session still logged in to account B after .claude.json switched to A
// sends A's key with B's window: it goes to its own account, stable across
// renders, and does not touch A's state or label.
func TestStaleKeyWithOtherWindow(t *testing.T) {
	a := newApp()
	endA := at(2026, time.August, 25, 12, 0)
	endB := at(2026, time.August, 21, 7, 0)
	stale := AccountInfo{Key: acctA.Key, Label: "someone-else", Plan: "claude_pro"}
	for i := 0; i < 4; i++ {
		now := at(2026, time.August, 19, 9, 0).Add(time.Duration(i) * time.Minute)
		a.sampleAccount(acctA, float64(20+i), endA, now)
		a.sampleAccount(stale, float64(90+i), endB, now.Add(time.Second))
	}
	if len(a.st.Accounts) != 2 {
		t.Fatalf("accounts = %v, want 2", a.st.sortedKeys())
	}
	accA := a.st.Accounts[acctA.Key]
	if accA.WindowEndUnix != endA.Unix() || accA.Label != acctA.Label || accA.UsedPct != 23 {
		t.Errorf("A = end %d label %q used %v, want untouched", accA.WindowEndUnix, accA.Label, accA.UsedPct)
	}
	if d := todaySpent(t, a, acctA.Key, "2026-08-19"); d.CarrySpent != 0 || spentOf(d) != 3 {
		t.Errorf("A carry=%v spent=%v, want 0 and 3", d.CarrySpent, spentOf(d))
	}
	for k, acc := range a.st.Accounts {
		if k != acctA.Key && (acc.Label != "" || spentOf(acc.Days["2026-08-19"]) != 3) {
			t.Errorf("stale account %s: label %q spent %v", k, acc.Label, spentOf(acc.Days["2026-08-19"]))
		}
	}
}

// Old statusline (no key): A→B→A is told apart by the window.
func TestKeylessSwitchFallsBackToWindow(t *testing.T) {
	a := newApp()
	endA := at(2026, time.August, 25, 12, 0)
	endB := at(2026, time.August, 22, 18, 0)

	a.sample(10, endA, at(2026, time.August, 19, 9, 0))
	a.sample(14, endA, at(2026, time.August, 19, 10, 0))
	a.sample(50, endB, at(2026, time.August, 19, 11, 0))
	a.sample(55, endB, at(2026, time.August, 19, 12, 0))
	a.sample(16, endA, at(2026, time.August, 19, 13, 0))

	if a.st.Active != legacyKey {
		t.Errorf("active = %q, want %q", a.st.Active, legacyKey)
	}
	if got := spentOf(todaySpent(t, a, legacyKey, "2026-08-19")); got != 6 {
		t.Errorf("legacy spent = %v, want 6", got)
	}
	if len(a.st.Accounts) != 2 {
		t.Fatalf("accounts = %v, want legacy + one anonymous", a.st.sortedKeys())
	}
}

// Old statusline: the normal weekly rollover stays a rollover of the same
// account, with the reset-day carry intact.
func TestKeylessRollover(t *testing.T) {
	a := newApp()
	oldEnd := at(2026, time.August, 18, 12, 0)
	newEnd := at(2026, time.August, 25, 12, 0)
	a.sample(70, oldEnd, at(2026, time.August, 18, 9, 0))
	a.sample(78, oldEnd, at(2026, time.August, 18, 11, 55))
	a.sample(2, newEnd, at(2026, time.August, 18, 12, 5))

	if len(a.st.Accounts) != 1 {
		t.Fatalf("accounts = %v, want 1", a.st.sortedKeys())
	}
	if d := todaySpent(t, a, legacyKey, "2026-08-18"); d.CarrySpent != 8 {
		t.Errorf("carry = %v, want 8", d.CarrySpent)
	}
}

func TestReadInputAccount(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	rl := `"rate_limits":{"seven_day":{"used_percentage":12,"resets_at":1787990400}}`

	_, _, acct, ok := readInput(write("old.json", `{`+rl+`}`))
	if !ok || acct != (AccountInfo{}) {
		t.Errorf("old snapshot: ok=%v acct=%+v, want ok and no account", ok, acct)
	}
	used, _, acct, ok := readInput(write("new.json",
		`{`+rl+`,"weekstat":{"account":{"key":"abc123def456","label":"Acme","plan":"claude_max"}}}`))
	want := AccountInfo{Key: "abc123def456", Label: "Acme", Plan: "claude_max"}
	if !ok || used != 12 || acct != want {
		t.Errorf("new snapshot: ok=%v used=%v acct=%+v", ok, used, acct)
	}
}

// A pre-accounts state file becomes the legacy account without losing days;
// the first keyed reading in the same window adopts it.
func TestMigrationFromOldStateFile(t *testing.T) {
	end := at(2026, time.August, 25, 12, 0)
	old := map[string]any{
		"window_end_unix":   end.Unix(),
		"window_start_unix": end.Add(-weekDur).Unix(),
		"used_pct":          30,
		"updated_unix":      at(2026, time.August, 19, 8, 0).Unix(),
		"days": map[string]any{
			"2026-08-18": map[string]any{"start_used": 10, "end_used": 25, "window_end": end.Unix()},
			"2026-08-19": map[string]any{"start_used": 25, "end_used": 30, "window_end": end.Unix()},
		},
	}
	b, _ := json.Marshal(old)
	p := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}

	st := loadState(p)
	if st.Active != legacyKey || len(st.Accounts[legacyKey].Days) != 2 {
		t.Fatalf("after load: active=%q accounts=%v", st.Active, st.sortedKeys())
	}

	a := &App{st: st}
	a.sampleAccount(acctA, 33, end, at(2026, time.August, 19, 9, 0))
	if _, ok := a.st.Accounts[legacyKey]; ok {
		t.Error("legacy not adopted by the first key in the same window")
	}
	d := todaySpent(t, a, acctA.Key, "2026-08-19")
	if d.StartUsed != 25 || spentOf(d) != 8 {
		t.Errorf("adopted day start=%v spent=%v, want 25 and 8", d.StartUsed, spentOf(d))
	}
	if _, ok := a.st.Accounts[acctA.Key].Days["2026-08-18"]; !ok {
		t.Error("yesterday lost in migration")
	}
}

// A key in another window leaves the legacy history alone.
func TestMigrationKeepsLegacyForOtherWindow(t *testing.T) {
	a := newApp()
	a.sample(10, at(2026, time.August, 25, 12, 0), at(2026, time.August, 19, 9, 0))
	a.sampleAccount(acctB, 40, at(2026, time.August, 22, 18, 0), at(2026, time.August, 19, 10, 0))
	if _, ok := a.st.Accounts[legacyKey]; !ok {
		t.Error("legacy history dropped")
	}
	if len(a.st.Accounts) != 2 {
		t.Errorf("accounts = %v, want 2", a.st.sortedKeys())
	}
}

func TestStateRoundTrip(t *testing.T) {
	a := newApp()
	a.sampleAccount(acctA, 10, at(2026, time.August, 25, 12, 0), at(2026, time.August, 19, 9, 0))
	a.sampleAccount(acctB, 40, at(2026, time.August, 22, 18, 0), at(2026, time.August, 19, 10, 0))
	p := filepath.Join(t.TempDir(), "state.json")
	if err := writeJSONAtomic(p, a.st); err != nil {
		t.Fatal(err)
	}
	st := loadState(p)
	if st.Active != acctB.Key || len(st.Accounts) != 2 || st.UsedPct != 40 {
		t.Errorf("reloaded active=%q accounts=%d used=%v", st.Active, len(st.Accounts), st.UsedPct)
	}
	if st.Accounts[acctA.Key].Label != "Acme" {
		t.Errorf("label lost: %+v", st.Accounts[acctA.Key])
	}
}

func TestPlanLabel(t *testing.T) {
	cases := map[string]string{
		"default_claude_max_20x": "max 20x",
		"claude_max":             "max",
		"claude_pro":             "pro",
		"":                       "",
		"enterprise":             "enterprise",
	}
	for in, want := range cases {
		if got := planLabel(in); got != want {
			t.Errorf("planLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDisplayLabel(t *testing.T) {
	st := newState()
	st.Accounts["k1"] = &AccountState{Label: "Acme", Plan: "claude_max"}
	st.Accounts["k2"] = &AccountState{Label: "Globex", Plan: "claude_pro"}
	st.Accounts["k3"] = &AccountState{Label: "Globex", Plan: "claude_max"}
	st.Accounts["k4"] = &AccountState{Plan: "claude_pro"}
	st.Accounts["k5"] = &AccountState{}
	want := map[string]string{
		"k1": "Acme",
		"k2": "pro · k2", // shared name
		"k3": "max · k3",
		"k4": "pro · k4", // no name
		"k5": "k5",       // nothing at all
	}
	for k, w := range want {
		if got := st.displayLabel(k); got != w {
			t.Errorf("displayLabel(%s) = %q, want %q", k, got, w)
		}
	}
}

// week-stats.json keeps every pre-accounts field; the account block is
// additive and absent until an account is seen.
func TestOutputAccountBlock(t *testing.T) {
	now := at(2026, time.August, 19, 10, 0)
	b, _ := json.Marshal(computeOutput(newState(), now))
	if strings.Contains(string(b), `"account"`) {
		t.Errorf("empty state emits an account block: %s", b)
	}

	a := newApp()
	a.sampleAccount(acctA, 10, at(2026, time.August, 25, 12, 0), at(2026, time.August, 19, 9, 0))
	a.sampleAccount(acctB, 40, at(2026, time.August, 22, 18, 0), now)
	var got map[string]any
	b, _ = json.Marshal(computeOutput(a.st, now))
	_ = json.Unmarshal(b, &got)
	for _, f := range []string{"version", "updated_at", "has_data", "window", "quota", "today", "days", "service"} {
		if _, ok := got[f]; !ok {
			t.Errorf("field %q missing", f)
		}
	}
	acc, _ := got["account"].(map[string]any)
	if acc["key"] != acctB.Key || acc["label"] != "Globex" || acc["plan"] != "pro" {
		t.Errorf("account = %v", acc)
	}
	accts, _ := got["accounts"].(map[string]any)
	sumA, _ := accts[acctA.Key].(map[string]any)
	if sumA["label"] != "Acme" || sumA["active"] != false || sumA["used_pct"] != 10.0 {
		t.Errorf("accounts[A] = %v", sumA)
	}
	// each account carries its own window, for the all-accounts overview
	if sumA["has_data"] != true || sumA["window_start"] != "2026-08-18 12:00" || sumA["window_end"] != "2026-08-25 12:00" ||
		sumA["resets_in_hours"] != 146.0 || sumA["elapsed_pct"] != 13.1 {
		t.Errorf("accounts[A] window = %v", sumA)
	}
	sumB, _ := accts[acctB.Key].(map[string]any)
	if sumB["window_end"] != "2026-08-22 18:00" || sumB["resets_in_hours"] != 80.0 {
		t.Errorf("accounts[B] window = %v", sumB)
	}
}

func TestHTTPAccountParam(t *testing.T) {
	a := newApp()
	now := time.Now()
	a.sampleAccount(acctA, 10, now.Add(72*time.Hour), now.Add(-time.Minute))
	a.sampleAccount(acctB, 40, now.Add(24*time.Hour), now)

	get := func(h http.HandlerFunc, url string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest("GET", url, nil))
		return rec
	}

	var o Output
	rec := get(a.handleStats, "/stats?account="+acctA.Key)
	_ = json.Unmarshal(rec.Body.Bytes(), &o)
	if rec.Code != 200 || o.Account == nil || o.Account.Key != acctA.Key || o.Quota.UsedPct != 10 {
		t.Errorf("/stats?account=A: code=%d account=%+v used=%v", rec.Code, o.Account, o.Quota.UsedPct)
	}
	rec = get(a.handleStats, "/stats")
	_ = json.Unmarshal(rec.Body.Bytes(), &o)
	if o.Account.Key != acctB.Key {
		t.Errorf("/stats default account = %s, want active B", o.Account.Key)
	}
	if rec := get(a.handleStats, "/stats?account=nope"); rec.Code != 404 {
		t.Errorf("/stats unknown account: code %d, want 404", rec.Code)
	}
	if rec := get(a.handleHistory, "/history?account=nope"); rec.Code != 404 {
		t.Errorf("/history unknown account: code %d, want 404", rec.Code)
	}
	var h History
	_ = json.Unmarshal(get(a.handleHistory, "/history?account="+acctA.Key).Body.Bytes(), &h)
	if len(h.Records) != 1 || h.Records[0].UsedEod != 10 {
		t.Errorf("/history?account=A = %+v", h.Records)
	}
	var list []AccountSummary
	_ = json.Unmarshal(get(a.handleAccounts, "/accounts").Body.Bytes(), &list)
	if len(list) != 2 || list[0].Key != acctB.Key || !list[0].Active {
		t.Errorf("/accounts = %+v, want B (active) first", list)
	}
}

// r1: once A's window has ended, a keyless reading is A's next week only if
// its window ends a week after A's; another account's window is a new account.
func TestKeylessRolloverNeedsNextWeek(t *testing.T) {
	a := newApp()
	endA := at(2026, time.August, 18, 12, 0)
	a.sample(70, endA, at(2026, time.August, 18, 9, 0))

	other := at(2026, time.August, 21, 7, 0) // B's window, seen after A's ended
	a.sample(40, other, at(2026, time.August, 18, 13, 0))
	if a.st.Accounts[legacyKey].WindowEndUnix != endA.Unix() {
		t.Error("another account's reading rolled A over")
	}
	if len(a.st.Accounts) != 2 {
		t.Fatalf("accounts = %v, want legacy + anonymous", a.st.sortedKeys())
	}

	a.sample(2, endA.Add(weekDur), at(2026, time.August, 18, 14, 0))
	if got := a.st.Accounts[legacyKey].WindowEndUnix; got != endA.Add(weekDur).Unix() {
		t.Errorf("A's next week went elsewhere: legacy window = %d", got)
	}
	if len(a.st.Accounts) != 2 {
		t.Errorf("accounts = %v, want still 2", a.st.sortedKeys())
	}
}

// r2: the first key, seen after the legacy window ended, adopts legacy when
// its window is legacy's next week — no phantom "legacy" left behind.
func TestKeyAdoptsExpiredLegacy(t *testing.T) {
	a := newApp()
	endA := at(2026, time.August, 18, 12, 0)
	a.sample(70, endA, at(2026, time.August, 18, 9, 0))
	a.sampleAccount(acctA, 3, endA.Add(weekDur), at(2026, time.August, 19, 9, 0))

	if _, ok := a.st.Accounts[legacyKey]; ok || len(a.st.Accounts) != 1 {
		t.Errorf("accounts = %v, want only %s", a.st.sortedKeys(), acctA.Key)
	}
	if _, ok := a.st.Accounts[acctA.Key].Days["2026-08-18"]; !ok {
		t.Error("legacy history lost on adoption")
	}

	// but a key in an unrelated window leaves an expired legacy alone
	b := newApp()
	b.sample(70, endA, at(2026, time.August, 18, 9, 0))
	b.sampleAccount(acctB, 3, at(2026, time.August, 23, 7, 0), at(2026, time.August, 19, 9, 0))
	if _, ok := b.st.Accounts[legacyKey]; !ok {
		t.Error("legacy adopted by a key from another window")
	}
}

// r3: a keyless reading (jq failed on a half-written .claude.json) at the
// weekly rollover of a keyed account rolls that account over.
func TestKeylessRollsOverKeyedAccount(t *testing.T) {
	a := newApp()
	endA := at(2026, time.August, 18, 12, 0)
	a.sampleAccount(acctA, 70, endA, at(2026, time.August, 18, 9, 0))
	a.sample(2, endA.Add(weekDur), at(2026, time.August, 18, 12, 5))

	if len(a.st.Accounts) != 1 || a.st.Active != acctA.Key {
		t.Fatalf("accounts = %v active = %s, want only A", a.st.sortedKeys(), a.st.Active)
	}
	if got := a.st.Accounts[acctA.Key].WindowEndUnix; got != endA.Add(weekDur).Unix() {
		t.Errorf("A not rolled over: window = %d", got)
	}
	// keyless inside the window keeps landing on A as well
	a.sample(4, endA.Add(weekDur), at(2026, time.August, 18, 13, 0))
	if len(a.st.Accounts) != 1 {
		t.Errorf("accounts = %v, want only A", a.st.sortedKeys())
	}
}

// r4/r10: the label is the email; an auto-generated "<x>'s Organization" is dropped.
func TestAccountLabelEmailAndOrgFilter(t *testing.T) {
	cases := map[string]string{
		"jane@example.com's Organization": "", // auto-generated personal org name
		"Jane Doe's Organization":         "",
		"jane@example.com":                "jane@example.com", // the email is the wanted label
		"Acme":                            "Acme",
	}
	for in, want := range cases {
		if got := accountLabel(in); got != want {
			t.Errorf("accountLabel(%q) = %q, want %q", in, got, want)
		}
	}

	a := newApp()
	end := at(2026, time.August, 25, 12, 0)
	acct := AccountInfo{Key: "cccccccccccc", Label: "jane@example.com's Organization", Plan: "claude_pro"}
	a.sampleAccount(acct, 10, end, at(2026, time.August, 19, 9, 0))
	if a.st.Accounts[acct.Key].Label != "" {
		t.Errorf("org-name label stored: %q", a.st.Accounts[acct.Key].Label)
	}
	if o := computeOutput(a.st, at(2026, time.August, 19, 9, 0)); o.Account.Label != "pro · cccccccccccc" {
		t.Errorf("label = %q, want plan · key", o.Account.Label)
	}

	// the next snapshot carries the email: the stored label follows it
	acct.Label = "jane@example.com"
	a.sampleAccount(acct, 11, end, at(2026, time.August, 19, 10, 0))
	if o := computeOutput(a.st, at(2026, time.August, 19, 10, 0)); o.Account.Label != "jane@example.com" {
		t.Errorf("label = %q, want the email", o.Account.Label)
	}

	// an org-name label saved by an earlier build is cleaned on load
	st := newState()
	st.Accounts["k1"] = &AccountState{Label: "Jane's Organization", Plan: "claude_max"}
	st.Active = "k1"
	st.normalize()
	if st.Accounts["k1"].Label != "" || st.displayLabel("k1") != "max · k1" {
		t.Errorf("stored org name survived load: %q / %q", st.Accounts["k1"].Label, st.displayLabel("k1"))
	}
}

// r5: the first write an older build can't fully read keeps a .bak — the
// pre-accounts file on migration — and never overwrites it.
func TestStateBackupOnMigration(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	old := `{"window_end_unix":1787990400,"window_start_unix":1787385600,"used_pct":5,"updated_unix":1,"days":{}}`
	if err := os.WriteFile(statePath, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &App{st: loadState(statePath), inputPath: filepath.Join(dir, "none.json"),
		outPath: filepath.Join(dir, "out.json"), statePath: statePath}
	a.pollOnce()
	b, err := os.ReadFile(statePath + ".bak")
	if err != nil || string(b) != old {
		t.Fatalf(".bak = %q, %v; want the pre-accounts file", b, err)
	}
	a.pollOnce() // later writes leave it alone
	if b, _ := os.ReadFile(statePath + ".bak"); string(b) != old {
		t.Error(".bak overwritten")
	}
}

func TestNeedsBackup(t *testing.T) {
	cases := []struct {
		file          bool
		loaded, accts int
		want          bool
	}{
		{false, 0, 2, false}, // fresh install: nothing to keep
		{true, 0, 1, true},   // migration
		{true, 1, 1, false},  // one account, as before
		{true, 1, 2, true},   // a second account appears
		{true, 2, 3, false},  // already multi-account on disk
	}
	for _, c := range cases {
		st := newState()
		st.loadedFile, st.loadedAccounts = c.file, c.loaded
		for i := 0; i < c.accts; i++ {
			st.Accounts[string(rune('a'+i))] = &AccountState{}
		}
		if got := st.needsBackup(); got != c.want {
			t.Errorf("%+v: needsBackup = %v", c, got)
		}
	}
}
