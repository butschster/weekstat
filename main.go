// weekstat — a tiny always-on daemon that turns Claude Code's rolling 7-day
// usage quota into persistent weekly statistics.
//
// It watches the file where the statusline dumps its stdin snapshot
// (~/.claude/.statusline-input.json), reads rate_limits.seven_day, and keeps:
//
//   - the current window boundaries (start / end / reset countdown),
//   - per-day consumption (delta of used% since the day's first reading),
//   - today's spend, the per-day budget that makes the quota last, and pace,
//
// writing it all to ~/.claude/week-stats.json (consumed by the statusline)
// and serving a live HTML dashboard + JSON API over HTTP.
//
// Stdlib only, no external dependencies.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
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

// version is stamped at build time via -ldflags "-X main.version=...".
var version = "dev"

// ---------------------------------------------------------------------------
// Persisted state (internal) — enough to resume day baselines after a restart.
// ---------------------------------------------------------------------------

type DayStat struct {
	StartUsed float64 `json:"start_used"` // used% at the first reading of the day
	EndUsed   float64 `json:"end_used"`   // used% at the latest reading
	StartUnix int64   `json:"start_unix"`
	EndUnix   int64   `json:"end_unix"`
}

type PrevWindow struct {
	Start        string   `json:"start"`
	End          string   `json:"end"`
	FinalUsedPct float64  `json:"final_used_pct"`
	Days         []DayOut `json:"days"`
}

type State struct {
	WindowEndUnix   int64               `json:"window_end_unix"`
	WindowStartUnix int64               `json:"window_start_unix"`
	UsedPct         float64             `json:"used_pct"`
	UpdatedUnix     int64               `json:"updated_unix"`
	Days            map[string]*DayStat `json:"days"`
	Previous        *PrevWindow         `json:"previous,omitempty"`
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
		Pace            string  `json:"pace"` // on_track | slightly_over | over
	} `json:"quota"`
	Today struct {
		Date           string  `json:"date"`
		StartUsedPct   float64 `json:"start_used_pct"`
		CurrentUsedPct float64 `json:"current_used_pct"`
		SpentPct       float64 `json:"spent_pct"`
	} `json:"today"`
	Days     []DayOut    `json:"days"`
	Previous *PrevWindow `json:"previous,omitempty"`
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
	st := &State{Days: map[string]*DayStat{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, st)
	}
	if st.Days == nil {
		st.Days = map[string]*DayStat{}
	}
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

// readInput pulls used% and reset time out of the statusline snapshot file.
func readInput(path string) (used float64, resetsAt time.Time, ok bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var in struct {
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
	return *sd.UsedPercentage, rt, true
}

func buildDays(days map[string]*DayStat, todayKey string) ([]DayOut, float64) {
	keys := make([]string, 0, len(days))
	for k := range days {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	total := 0.0
	out := make([]DayOut, 0, len(keys))
	for _, k := range keys {
		d := days[k]
		spent := d.EndUsed - d.StartUsed
		if spent < 0 {
			spent = 0
		}
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
	return out, round1(total)
}

func computeOutput(st *State, now time.Time) Output {
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
	o.Quota.BudgetPerDayPct = round1(remaining / daysLeft)
	switch {
	case used <= o.Window.ElapsedPct:
		o.Quota.Pace = "on_track"
	case used <= o.Window.ElapsedPct+10:
		o.Quota.Pace = "slightly_over"
	default:
		o.Quota.Pace = "over"
	}

	todayKey := now.Format("2006-01-02")
	o.Days, _ = buildDays(st.Days, todayKey)
	o.Previous = st.Previous

	o.Today.Date = todayKey
	if d, ok := st.Days[todayKey]; ok {
		sp := d.EndUsed - d.StartUsed
		if sp < 0 {
			sp = 0
		}
		o.Today.StartUsedPct = round1(d.StartUsed)
		o.Today.CurrentUsedPct = round1(d.EndUsed)
		o.Today.SpentPct = round1(sp)
	} else {
		o.Today.CurrentUsedPct = round1(used)
	}
	return o
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
	tmpl      *template.Template
}

// sample folds one reading into the state, rolling the window over when the
// reset time jumps (a new weekly period began).
func (a *App) sample(used float64, resetsAt time.Time, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()

	endUnix := resetsAt.Unix()
	if a.st.WindowEndUnix != 0 && math.Abs(float64(endUnix-a.st.WindowEndUnix)) > 3600 {
		// New window: archive the finished one, reset day baselines.
		prevDays, _ := buildDays(a.st.Days, "")
		a.st.Previous = &PrevWindow{
			Start:        time.Unix(a.st.WindowStartUnix, 0).Local().Format("2006-01-02 15:04"),
			End:          time.Unix(a.st.WindowEndUnix, 0).Local().Format("2006-01-02 15:04"),
			FinalUsedPct: round1(a.st.UsedPct),
			Days:         prevDays,
		}
		a.st.Days = map[string]*DayStat{}
	}

	a.st.WindowEndUnix = endUnix
	a.st.WindowStartUnix = resetsAt.Add(-weekDur).Unix()
	a.st.UsedPct = used
	a.st.UpdatedUnix = now.Unix()

	key := now.Format("2006-01-02")
	d, ok := a.st.Days[key]
	if !ok {
		d = &DayStat{StartUsed: used, StartUnix: now.Unix()}
		a.st.Days[key] = d
	}
	d.EndUsed = used
	d.EndUnix = now.Unix()
}

func (a *App) pollOnce() {
	now := time.Now()
	if used, reset, ok := readInput(a.inputPath); ok {
		a.sample(used, reset, now)
	}
	a.mu.RLock()
	out := computeOutput(a.st, now)
	stCopy := *a.st
	a.mu.RUnlock()

	if err := writeJSONAtomic(a.outPath, out); err != nil {
		log.Printf("write stats: %v", err)
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

type dayView struct {
	DayOut
	BarStyle template.HTMLAttr
	Today    bool
}

type view struct {
	O          Output
	PaceColor  string
	PaceLabel  string
	TodayColor string
	UsedBar    template.HTMLAttr
	ElapsedBar template.HTMLAttr
	Days       []dayView
}

func barStyle(w float64) template.HTMLAttr {
	w = clamp(w, 0, 100)
	return template.HTMLAttr(fmt.Sprintf(`style="width:%.1f%%"`, w))
}

func paceMeta(pace string) (color, label string) {
	switch pace {
	case "on_track":
		return "#22c55e", "on track"
	case "slightly_over":
		return "#eab308", "slightly over"
	case "over":
		return "#ef4444", "over budget"
	}
	return "#94a3b8", "—"
}

func (a *App) buildView(now time.Time) view {
	a.mu.RLock()
	o := computeOutput(a.st, now)
	a.mu.RUnlock()

	pc, pl := paceMeta(o.Quota.Pace)
	tc := "#22c55e"
	if o.Quota.BudgetPerDayPct > 0 {
		switch {
		case o.Today.SpentPct > o.Quota.BudgetPerDayPct:
			tc = "#ef4444"
		case o.Today.SpentPct > o.Quota.BudgetPerDayPct*0.8:
			tc = "#eab308"
		}
	}
	v := view{O: o, PaceColor: pc, PaceLabel: pl, TodayColor: tc,
		UsedBar: barStyle(o.Quota.UsedPct), ElapsedBar: barStyle(o.Window.ElapsedPct)}
	for _, d := range o.Days {
		v.Days = append(v.Days, dayView{DayOut: d, BarStyle: barStyle(d.SharePct), Today: d.IsToday})
	}
	return v
}

func (a *App) handleHTML(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.tmpl.Execute(w, a.buildView(time.Now())); err != nil {
		log.Printf("template: %v", err)
	}
}

func (a *App) handleStats(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	o := computeOutput(a.st, time.Now())
	a.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(o)
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
	flag.Parse()

	app := &App{
		st:        loadState(*statef),
		inputPath: *input,
		outPath:   *out,
		statePath: *statef,
		tmpl:      template.Must(template.New("dash").Parse(dashboardHTML)),
	}

	go app.loop(*interval)

	mux := http.NewServeMux()
	mux.HandleFunc("/", app.handleHTML)
	mux.HandleFunc("/stats", app.handleStats)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })

	log.Printf("weekstat %s: watching %s → %s, dashboard on http://%s", version, *input, *out, *addr)
	srv := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

// ---------------------------------------------------------------------------
// Dashboard template (self-contained, auto-refreshing).
// ---------------------------------------------------------------------------

const dashboardHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="10">
<title>weekstat · weekly quota</title>
<style>
  :root { color-scheme: dark; }
  * { box-sizing: border-box; }
  body { margin:0; font:15px/1.5 system-ui,-apple-system,Segoe UI,Roboto,sans-serif;
         background:#0b0f17; color:#e5e7eb; padding:28px; }
  .wrap { max-width:860px; margin:0 auto; }
  h1 { font-size:15px; font-weight:600; letter-spacing:.04em; text-transform:uppercase;
       color:#94a3b8; margin:0 0 4px; }
  .sub { color:#64748b; font-size:13px; margin-bottom:24px; }
  .grid { display:grid; grid-template-columns:repeat(auto-fit,minmax(160px,1fr)); gap:14px; margin-bottom:26px; }
  .card { background:#111827; border:1px solid #1f2937; border-radius:12px; padding:16px 18px; }
  .card .k { font-size:12px; color:#94a3b8; text-transform:uppercase; letter-spacing:.05em; }
  .card .v { font-size:30px; font-weight:700; margin-top:6px; line-height:1; }
  .card .v small { font-size:15px; font-weight:500; color:#64748b; }
  .badge { display:inline-block; padding:3px 12px; border-radius:999px; font-size:13px; font-weight:600; }
  .bar { height:8px; background:#1f2937; border-radius:999px; overflow:hidden; margin-top:12px; }
  .bar > span { display:block; height:100%; border-radius:999px; }
  table { width:100%; border-collapse:collapse; margin-top:10px; }
  th,td { text-align:left; padding:9px 10px; border-bottom:1px solid #1f2937; font-size:14px; }
  th { color:#94a3b8; font-weight:500; font-size:12px; text-transform:uppercase; letter-spacing:.04em; }
  td.num { text-align:right; font-variant-numeric:tabular-nums; }
  .daybar { height:6px; background:#1f2937; border-radius:999px; overflow:hidden; min-width:80px; }
  .daybar > span { display:block; height:100%; background:#3b82f6; }
  tr.today td { background:#0f1b2d; }
  tr.today td:first-child::after { content:" ·today"; color:#3b82f6; font-size:11px; }
  .win { color:#cbd5e1; font-size:14px; margin-bottom:18px; }
  .win b { color:#e5e7eb; }
  .foot { color:#475569; font-size:12px; margin-top:26px; }
  .empty { color:#64748b; padding:40px 0; text-align:center; }
</style>
</head>
<body>
<div class="wrap">
  <h1>Claude Code weekly quota</h1>
  <div class="sub">updated {{.O.UpdatedAt}} · page auto-refreshes every 10&nbsp;s</div>

{{if not .O.HasData}}
  <div class="empty">No data yet.<br>The daemon is waiting for the first <code>rate_limits.seven_day</code> snapshot from the statusline.</div>
{{else}}
  <div class="win">
    Window: <b>{{.O.Window.Start}}</b> → <b>{{.O.Window.End}}</b>
    · resets in <b>{{printf "%.1f" .O.Window.ResetsInHours}}&nbsp;h</b>
    · {{printf "%.0f" .O.Window.ElapsedPct}}% elapsed
  </div>

  <div class="grid">
    <div class="card">
      <div class="k">Used</div>
      <div class="v">{{printf "%.1f" .O.Quota.UsedPct}}<small>%</small></div>
      <div class="bar"><span {{.UsedBar}} style="background:#3b82f6"></span></div>
    </div>
    <div class="card">
      <div class="k">Left</div>
      <div class="v">{{printf "%.0f" .O.Quota.RemainingPct}}<small>%</small></div>
    </div>
    <div class="card">
      <div class="k">Spent today</div>
      <div class="v" style="color:{{.TodayColor}}">{{printf "%.1f" .O.Today.SpentPct}}<small>%</small></div>
    </div>
    <div class="card">
      <div class="k">Budget / day</div>
      <div class="v">{{printf "%.1f" .O.Quota.BudgetPerDayPct}}<small>%/d · {{printf "%.1f" .O.Quota.DaysLeft}}d</small></div>
    </div>
    <div class="card">
      <div class="k">Pace</div>
      <div class="v"><span class="badge" style="background:{{.PaceColor}};color:#0b0f17">{{.PaceLabel}}</span></div>
      <div class="bar"><span {{.ElapsedBar}} style="background:#475569"></span></div>
    </div>
  </div>

  <h1>Per-day breakdown</h1>
  <table>
    <thead><tr><th>Day</th><th class="num">used at end</th><th class="num">spent</th><th>share of week</th><th class="num">%</th></tr></thead>
    <tbody>
    {{range .Days}}
      <tr class="{{if .Today}}today{{end}}">
        <td>{{.Date}}</td>
        <td class="num">{{printf "%.1f" .EndUsed}}%</td>
        <td class="num">{{printf "%.1f" .SpentPct}}%</td>
        <td><div class="daybar"><span {{.BarStyle}}></span></div></td>
        <td class="num">{{printf "%.0f" .SharePct}}%</td>
      </tr>
    {{else}}
      <tr><td colspan="5" style="color:#64748b">no days in the current window yet</td></tr>
    {{end}}
    </tbody>
  </table>

  {{with .O.Previous}}
  <h1 style="margin-top:26px">Previous window</h1>
  <div class="win">{{.Start}} → {{.End}} · final used <b>{{printf "%.1f" .FinalUsedPct}}%</b></div>
  {{end}}
{{end}}

  <div class="foot">weekstat {{.O.Version}} · JSON API: <code>/stats</code> · health: <code>/healthz</code></div>
</div>
</body>
</html>`
