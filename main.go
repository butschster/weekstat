// weekstat — a tiny always-on daemon that turns Claude Code's rolling 7-day
// usage quota into persistent weekly statistics.
//
// It watches the file where the statusline dumps its stdin snapshot
// (~/.claude/.statusline-input.json), reads rate_limits.seven_day, and keeps:
//
//   - the current window boundaries (start / end / reset countdown),
//   - per-day consumption (delta of used% since the day's first reading),
//   - today's spend, the per-day budget that makes the quota last, and pace,
//   - a rolling multi-window daily history for the dashboard charts.
//
// Figures are kept per Claude account (the statusline script tags each snapshot
// with a hashed account key — see accounts.go), so switching accounts is not
// mistaken for a window reset.
//
// It writes ~/.claude/week-stats.json (consumed by the statusline) and serves a
// live self-contained HTML dashboard + JSON API (/stats, /history, /accounts) over HTTP.
//
// Stdlib only, no external dependencies.
package main

import (
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"
)

const weekDur = 7 * 24 * time.Hour

// keep at most this many days of per-day history (feeds /history charts).
const historyDays = 120

// version is stamped at build time via -ldflags "-X main.version=...".
var version = "dev"

//go:embed dashboard.html
var dashboardHTML []byte

// ---------------------------------------------------------------------------
// Persisted state (internal) — enough to resume day baselines after a restart.
// ---------------------------------------------------------------------------

type DayStat struct {
	StartUsed  float64 `json:"start_used"` // used% at the first reading of the day (re-baselined on a mid-day window reset)
	EndUsed    float64 `json:"end_used"`   // used% at the latest reading
	StartUnix  int64   `json:"start_unix"`
	EndUnix    int64   `json:"end_unix"`
	WindowEnd  int64   `json:"window_end"`            // reset epoch of the window this day belongs to
	CarrySpent float64 `json:"carry_spent,omitempty"` // spend banked from windows that ended earlier this same day
}

// AccountState is one account's quota window and its per-day history.
type AccountState struct {
	Label           string              `json:"label,omitempty"` // email, else organization name, as sent by the statusline
	Plan            string              `json:"plan,omitempty"`  // raw plan / rate-limit tier, as sent by the statusline
	WindowEndUnix   int64               `json:"window_end_unix"`
	WindowStartUnix int64               `json:"window_start_unix"`
	UsedPct         float64             `json:"used_pct"`
	UpdatedUnix     int64               `json:"updated_unix"`
	Days            map[string]*DayStat `json:"days"` // ALL days, across windows (pruned to historyDays)
}

// State holds every account seen. The embedded AccountState mirrors the
// active account so the file stays readable by pre-accounts builds.
type State struct {
	AccountState
	Active   string                   `json:"active,omitempty"`
	Accounts map[string]*AccountState `json:"accounts,omitempty"`

	// what was on disk at load, for the one-off backup (see needsBackup)
	loadedFile     bool
	loadedAccounts int
}

// ---------------------------------------------------------------------------
// Derived output (public) — written to week-stats.json and served over HTTP.
// ---------------------------------------------------------------------------

type DayOut struct {
	Date      string  `json:"date"`
	StartUsed float64 `json:"start_used_pct"`
	EndUsed   float64 `json:"end_used_pct"`
	SpentPct  float64 `json:"spent_pct"`
	SharePct  float64 `json:"share_pct"` // share of the window's total consumption
	IsToday   bool    `json:"is_today"`
}

type Output struct {
	Version   string `json:"version"`
	UpdatedAt string `json:"updated_at"`
	HasData   bool   `json:"has_data"`
	Window    struct {
		Start         string  `json:"start"`
		End           string  `json:"end"`
		ResetsInHours float64 `json:"resets_in_hours"`
		ElapsedPct    float64 `json:"elapsed_pct"`
	} `json:"window"`
	Quota struct {
		UsedPct         float64 `json:"used_pct"`
		RemainingPct    float64 `json:"remaining_pct"`
		BudgetPerDayPct float64 `json:"budget_per_day_pct"`
		DaysLeft        float64 `json:"days_left"`
		Pace            string  `json:"pace"` // on_track | slightly_over | over_budget
	} `json:"quota"`
	Today struct {
		Date           string  `json:"date"`
		StartUsedPct   float64 `json:"start_used_pct"`
		CurrentUsedPct float64 `json:"current_used_pct"`
		SpentPct       float64 `json:"spent_pct"`
		// SpentInWindowPct is today's spend inside the current window only.
		// It differs from spent_pct on a reset day, where spent_pct also
		// includes what was burned in the old window before the reset.
		SpentInWindowPct float64 `json:"spent_in_window_pct"`
		BudgetPct        float64 `json:"budget_pct"` // today's allowance (== quota.budget_per_day_pct)
		LeftPct          float64 `json:"left_pct"`   // budget − spent-in-window (negative = overspent)
	} `json:"today"`
	Days []DayOut `json:"days"`
	// Service is the Claude Code health from status.claude.com. When
	// service.outage is true, consumers hide the usage figures and show an
	// "API is down" badge instead — see status.go.
	Service ServiceStatus `json:"service"`
	// Account is the account these figures belong to; Accounts summarises
	// every known account by key. Both are absent until an account is seen.
	Account  *AccountRef               `json:"account,omitempty"`
	Accounts map[string]AccountSummary `json:"accounts,omitempty"`
}

// HistRecord is one day in the /history time-series (may span several windows).
type HistRecord struct {
	Date           string  `json:"date"`
	SpentPct       float64 `json:"spent_pct"`
	UsedEod        float64 `json:"used_pct_eod"`
	WindowID       string  `json:"window_id"`
	WindowElapsedH float64 `json:"window_elapsed_h"`
	IsWindowStart  bool    `json:"is_window_start"`
	IsToday        bool    `json:"is_today"`
}

type History struct {
	Days        int          `json:"days"`
	WindowHours float64      `json:"window_hours"`
	Records     []HistRecord `json:"records"`
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func round1(x float64) float64 { return math.Round(x*10) / 10 }

func clamp(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

func writeJSONAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func loadState(path string) *State {
	st := newState()
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, st)
		st.loadedFile = true
		st.loadedAccounts = len(st.Accounts)
	}
	st.normalize()
	return st
}

// parseResets accepts either an epoch (seconds or milliseconds) number or an
// RFC3339 / epoch string — Claude Code's exact encoding isn't contractual.
func parseResets(raw json.RawMessage) (time.Time, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return time.Time{}, false
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil && f > 0 {
		if f > 1e12 { // milliseconds
			f /= 1000
		}
		return time.Unix(int64(f), 0), true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil && s != "" {
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05Z07:00"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t, true
			}
		}
		if n, err := strconv.ParseFloat(s, 64); err == nil && n > 0 {
			if n > 1e12 {
				n /= 1000
			}
			return time.Unix(int64(n), 0), true
		}
	}
	return time.Time{}, false
}

// readInput pulls used%, reset time and the account (when the statusline
// script provides one) out of the statusline snapshot file.
func readInput(path string) (used float64, resetsAt time.Time, acct AccountInfo, ok bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var in struct {
		Weekstat struct {
			Account AccountInfo `json:"account"`
		} `json:"weekstat"`
		RateLimits struct {
			SevenDay struct {
				UsedPercentage *float64        `json:"used_percentage"`
				ResetsAt       json.RawMessage `json:"resets_at"`
			} `json:"seven_day"`
		} `json:"rate_limits"`
	}
	if err := json.Unmarshal(b, &in); err != nil {
		return
	}
	sd := in.RateLimits.SevenDay
	if sd.UsedPercentage == nil {
		return
	}
	rt, rok := parseResets(sd.ResetsAt)
	if !rok {
		return
	}
	return *sd.UsedPercentage, rt, in.Weekstat.Account, true
}

// windowSpentOf is the day's spend within its current window: the delta since
// the day's (possibly re-baselined) first reading, never negative.
func windowSpentOf(d *DayStat) float64 {
	s := d.EndUsed - d.StartUsed
	if s < 0 {
		s = 0
	}
	return s
}

// spentOf is the day's full calendar-day spend: the current window's delta plus
// whatever was banked from windows that ended earlier the same day.
func spentOf(d *DayStat) float64 {
	return d.CarrySpent + windowSpentOf(d)
}

// sameWindow treats two reset epochs as the same window if they are within an
// hour of each other, so encoding jitter in resets_at doesn't churn the state.
func sameWindow(a, b int64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= 3600
}

// buildDays renders a set of days (already scoped to one window) into DayOut.
func buildDays(days map[string]*DayStat, todayKey string) []DayOut {
	keys := make([]string, 0, len(days))
	for k := range days {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	total := 0.0
	out := make([]DayOut, 0, len(keys))
	for _, k := range keys {
		d := days[k]
		spent := spentOf(d)
		total += spent
		out = append(out, DayOut{
			Date:      k,
			StartUsed: round1(d.StartUsed),
			EndUsed:   round1(d.EndUsed),
			SpentPct:  round1(spent),
			IsToday:   k == todayKey,
		})
	}
	if total > 0 {
		for i := range out {
			out[i].SharePct = round1(out[i].SpentPct / total * 100)
		}
	}
	return out
}

// windowOutput renders one account's window into the public figures.
func windowOutput(st *AccountState, now time.Time) Output {
	var o Output
	o.Version = version
	o.UpdatedAt = now.Local().Format("2006-01-02 15:04:05")
	if st.WindowEndUnix == 0 {
		return o // no data yet
	}
	o.HasData = true

	end := time.Unix(st.WindowEndUnix, 0)
	start := time.Unix(st.WindowStartUnix, 0)
	o.Window.Start = start.Local().Format("2006-01-02 15:04")
	o.Window.End = end.Local().Format("2006-01-02 15:04")
	resetsH := end.Sub(now).Hours()
	o.Window.ResetsInHours = round1(resetsH)
	o.Window.ElapsedPct = round1(clamp((1-end.Sub(now).Seconds()/weekDur.Seconds())*100, 0, 100))

	used := st.UsedPct
	remaining := clamp(100-used, 0, 100)
	daysLeft := math.Max(0.25, resetsH/24)
	o.Quota.UsedPct = round1(used)
	o.Quota.RemainingPct = round1(remaining)
	o.Quota.DaysLeft = round1(daysLeft)

	// Stable daily budget. Instead of "quota left right now / fractional days
	// left" (which drops the instant you spend anything today and drifts as the
	// clock ticks), base it on the quota left at the START of today spread over
	// the whole days remaining. "Days remaining" = calendar days from today to
	// the reset date (today included; the partial reset-day morning is not
	// counted as its own day) — e.g. Wed with a Mon reset ⇒ Wed,Thu,Fri,Sat,Sun
	// = 5. Spending within today's share does not shrink the budget; only
	// carrying an over/under balance into tomorrow moves it, at the day boundary.
	todayKey := now.Format("2006-01-02")
	startUsed := used
	// Only trust today's baseline if it belongs to the active window — a stale
	// pre-reset baseline would wildly understate the quota left.
	if d, ok := st.Days[todayKey]; ok && sameWindow(d.WindowEnd, st.WindowEndUnix) {
		startUsed = d.StartUsed
	}
	remainStart := clamp(100-startUsed, 0, 100)
	loc := now.Location()
	yr, mo, dy := now.Date()
	today0 := time.Date(yr, mo, dy, 0, 0, 0, 0, loc)
	ey, em, ed := end.Date()
	reset0 := time.Date(ey, em, ed, 0, 0, 0, 0, loc)
	daysRemaining := int(math.Round(reset0.Sub(today0).Hours() / 24))
	if daysRemaining < 1 {
		daysRemaining = 1
	}
	o.Quota.BudgetPerDayPct = round1(remainStart / float64(daysRemaining))
	switch {
	case used <= o.Window.ElapsedPct:
		o.Quota.Pace = "on_track"
	case used <= o.Window.ElapsedPct+10:
		o.Quota.Pace = "slightly_over"
	default:
		o.Quota.Pace = "over_budget"
	}

	// current-window breakdown = days tagged with the active window's reset.
	curr := make(map[string]*DayStat)
	for k, d := range st.Days {
		if sameWindow(d.WindowEnd, st.WindowEndUnix) {
			curr[k] = d
		}
	}
	o.Days = buildDays(curr, todayKey)

	o.Today.Date = todayKey
	o.Today.BudgetPct = o.Quota.BudgetPerDayPct
	if d, ok := st.Days[todayKey]; ok {
		o.Today.StartUsedPct = round1(d.StartUsed)
		o.Today.CurrentUsedPct = round1(d.EndUsed)
		o.Today.SpentPct = round1(spentOf(d))
		o.Today.SpentInWindowPct = round1(windowSpentOf(d))
	} else {
		o.Today.CurrentUsedPct = round1(used)
	}
	// Today's allowance is a slice of the current window's quota, so overspend
	// is measured against the in-window part only — on a reset day the old
	// window's morning spend doesn't eat the fresh window's daily budget.
	o.Today.LeftPct = round1(o.Today.BudgetPct - o.Today.SpentInWindowPct)
	return o
}

// accountHistory builds one account's daily time-series for the charts (last `days` days).
func accountHistory(st *AccountState, now time.Time, days int) History {
	todayKey := now.Format("2006-01-02")
	cutoff := now.AddDate(0, 0, -(days - 1)).Format("2006-01-02")

	keys := make([]string, 0, len(st.Days))
	for k := range st.Days {
		if k >= cutoff {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	recs := make([]HistRecord, 0, len(keys))
	var prevWindow int64 = -1
	for _, k := range keys {
		d := st.Days[k]
		rec := HistRecord{
			Date:     k,
			SpentPct: round1(spentOf(d)),
			UsedEod:  round1(d.EndUsed),
			IsToday:  k == todayKey,
		}
		if d.WindowEnd > 0 {
			winStart := d.WindowEnd - int64(weekDur.Seconds())
			rec.WindowID = time.Unix(d.WindowEnd, 0).Local().Format("2006-01-02")
			rec.WindowElapsedH = round1(float64(d.EndUnix-winStart) / 3600)
		}
		if prevWindow != -1 && d.WindowEnd != prevWindow {
			rec.IsWindowStart = true
		}
		prevWindow = d.WindowEnd
		recs = append(recs, rec)
	}
	return History{Days: days, WindowHours: weekDur.Hours(), Records: recs}
}

// ---------------------------------------------------------------------------
// App
// ---------------------------------------------------------------------------

type App struct {
	mu        sync.RWMutex
	st        *State
	inputPath string
	outPath   string
	statePath string
	status    *statusChecker // nil when the status-page check is disabled
	backedUp  bool           // the state file's one-off .bak was taken
}

// output is the active account's figures plus the service-health block.
// Caller holds a.mu.
func (a *App) output(now time.Time) Output {
	return a.outputFor(a.st.Active, now)
}

// outputFor is output for a given account key. Caller holds a.mu.
func (a *App) outputFor(key string, now time.Time) Output {
	o := a.st.output(key, now)
	if a.status != nil {
		o.Service = a.status.current()
	} else {
		o.Service = ServiceStatus{Component: defaultStatusComponent, Status: "unknown", Level: levelUnknown, Label: "unknown", PageURL: defaultStatusPage}
	}
	return o
}

// sample folds one reading without an account key (an old statusline script);
// the account is guessed from the window — see State.route.
func (a *App) sample(used float64, resetsAt time.Time, now time.Time) {
	a.sampleAccount(AccountInfo{}, used, resetsAt, now)
}

// sampleAccount routes one reading to its account's state and makes that
// account the active one.
func (a *App) sampleAccount(info AccountInfo, used float64, resetsAt time.Time, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()

	key := a.st.route(info.Key, resetsAt.Unix(), now)
	acc := a.st.Accounts[key]
	if info.Key != "" {
		acc.Label = accountLabel(info.Label) // follows the latest snapshot
	}
	if info.Plan != "" {
		acc.Plan = info.Plan
	}
	acc.sample(used, resetsAt, now)
	a.st.Active = key
	a.st.AccountState = *acc
	a.st.pruneAccounts(now)
}

// transition is how a reading relates to the account's current window.
type transition int

const (
	transSame     transition = iota // same window: plain accumulation
	transRollover                   // the window changed (reset at the end of the week)
	// A manual early reset (same window, used% drops sharply) slots in here.
)

func (st *AccountState) classify(endUnix int64) transition {
	if sameWindow(st.WindowEndUnix, endUnix) {
		return transSame
	}
	return transRollover
}

// sample folds one reading into the account. Days accumulate across windows;
// each day is tagged with the reset epoch of the window it belongs to, so a
// reset is just a new tag rather than a wipe — the history survives for the charts.
func (st *AccountState) sample(used float64, resetsAt time.Time, now time.Time) {
	endUnix := resetsAt.Unix()
	if st.classify(endUnix) == transRollover {
		st.WindowEndUnix = endUnix
		st.WindowStartUnix = resetsAt.Add(-weekDur).Unix()
	}
	st.UsedPct = used
	st.UpdatedUnix = now.Unix()

	key := now.Format("2006-01-02")
	d, ok := st.Days[key]
	switch {
	case !ok:
		d = &DayStat{StartUsed: used, StartUnix: now.Unix()}
		st.Days[key] = d
	case d.WindowEnd != 0 && !sameWindow(d.WindowEnd, st.WindowEndUnix):
		// The weekly window reset mid-day: used% just dropped to the new
		// window's level. Bank what the old window's part of the day spent and
		// re-baseline the day at the new window's first reading — otherwise
		// today's spend reads 0 and the daily budget is computed from a stale
		// pre-reset baseline for the rest of the day.
		d.CarrySpent += windowSpentOf(d)
		d.StartUsed = used
		d.StartUnix = now.Unix()
	}
	d.EndUsed = used
	d.EndUnix = now.Unix()
	d.WindowEnd = st.WindowEndUnix

	// prune old history
	cutoff := now.AddDate(0, 0, -historyDays).Format("2006-01-02")
	for k := range st.Days {
		if k < cutoff {
			delete(st.Days, k)
		}
	}
}

func (a *App) pollOnce() {
	now := time.Now()
	if used, reset, acct, ok := readInput(a.inputPath); ok {
		a.sampleAccount(acct, used, reset, now)
	}
	a.mu.RLock()
	out := a.output(now)
	stCopy := *a.st
	a.mu.RUnlock()

	if err := writeJSONAtomic(a.outPath, out); err != nil {
		log.Printf("write stats: %v", err)
	}
	if !a.backedUp && stCopy.needsBackup() {
		backupState(a.statePath)
		a.backedUp = true
	}
	if err := writeJSONAtomic(a.statePath, &stCopy); err != nil {
		log.Printf("write state: %v", err)
	}
}

func (a *App) loop(interval time.Duration) {
	a.pollOnce()
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for range tick.C {
		a.pollOnce()
	}
}

// ---------------------------------------------------------------------------
// HTTP
// ---------------------------------------------------------------------------

func (a *App) handleHTML(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(dashboardHTML)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// accountParam returns the ?account= key and whether it names a known
// account (an empty key means the active one). Caller holds a.mu.
func (a *App) accountParam(r *http.Request) (string, bool) {
	key := r.URL.Query().Get("account")
	if key == "" {
		return a.st.Active, true
	}
	_, ok := a.st.Accounts[key]
	return key, ok
}

func (a *App) handleStats(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	key, ok := a.accountParam(r)
	o := a.outputFor(key, time.Now())
	a.mu.RUnlock()
	if !ok {
		http.Error(w, "unknown account", http.StatusNotFound)
		return
	}
	writeJSON(w, o)
}

func (a *App) handleAccounts(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	list := a.st.accountList(time.Now())
	a.mu.RUnlock()
	writeJSON(w, list)
}

func (a *App) handleHistory(w http.ResponseWriter, r *http.Request) {
	days := 30
	if q := r.URL.Query().Get("days"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 && n <= 365 {
			days = n
		}
	}
	a.mu.RLock()
	key, ok := a.accountParam(r)
	h := accountHistory(a.st.account(key), time.Now(), days)
	a.mu.RUnlock()
	if !ok {
		http.Error(w, "unknown account", http.StatusNotFound)
		return
	}
	writeJSON(w, h)
}

// handleService exposes just the status-page check (also embedded in /stats).
func (a *App) handleService(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	o := a.output(time.Now())
	a.mu.RUnlock()
	writeJSON(w, o.Service)
}

// ---------------------------------------------------------------------------

func main() {
	home, _ := os.UserHomeDir()
	def := func(p string) string { return filepath.Join(home, ".claude", p) }

	input := flag.String("input", def(".statusline-input.json"), "statusline stdin snapshot to watch")
	out := flag.String("out", def("week-stats.json"), "weekly stats JSON output (read by the statusline)")
	statef := flag.String("state", def(".weekstat-state.json"), "internal resume state file")
	addr := flag.String("addr", "127.0.0.1:7457", "HTTP listen address for the dashboard")
	interval := flag.Duration("interval", 5*time.Second, "poll interval")
	statusURL := flag.String("status-url", defaultStatusURL, "Statuspage summary endpoint for the Claude service health")
	statusComponent := flag.String("status-component", defaultStatusComponent, "status-page component to track")
	statusInterval := flag.Duration("status-interval", 5*time.Minute, "how often to poll the status page (0 disables the check)")
	flag.Parse()

	app := &App{
		st:        loadState(*statef),
		inputPath: *input,
		outPath:   *out,
		statePath: *statef,
	}

	if *statusInterval > 0 {
		app.status = newStatusChecker(*statusURL, *statusComponent)
		go app.status.loop(*statusInterval)
	}
	go app.loop(*interval)

	mux := http.NewServeMux()
	mux.HandleFunc("/", app.handleHTML)
	mux.HandleFunc("/stats", app.handleStats)
	mux.HandleFunc("/history", app.handleHistory)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })
	mux.HandleFunc("/service", app.handleService)
	mux.HandleFunc("/accounts", app.handleAccounts)

	log.Printf("weekstat %s: watching %s → %s, dashboard on http://%s", version, *input, *out, *addr)
	srv := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
