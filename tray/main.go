// weekstat-tray — a GNOME/AppIndicator top-panel indicator for weekstat.
//
// It shows a ring-gauge icon in the Ubuntu top panel (arc fills with used%,
// coloured by pace); the dropdown lists the live figures — today's spend, the
// per-day budget, used/left and the reset countdown — polled from the weekstat
// daemon's /stats endpoint. No browser needed; the numbers live in the panel.
//
// This is a SEPARATE module from the daemon so the daemon stays dependency-free;
// the tray needs a DBus StatusNotifierItem (fyne.io/systray) to reach the panel.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"os/exec"
	"time"

	"fyne.io/systray"
)

var version = "dev"

var (
	statsURL string
	dashURL  string
	client   = &http.Client{Timeout: 4 * time.Second}
)

type stats struct {
	HasData bool `json:"has_data"`
	Window  struct {
		ResetsInHours float64 `json:"resets_in_hours"`
	} `json:"window"`
	Quota struct {
		UsedPct         float64 `json:"used_pct"`
		RemainingPct    float64 `json:"remaining_pct"`
		BudgetPerDayPct float64 `json:"budget_per_day_pct"`
		DaysLeft        float64 `json:"days_left"`
		Pace            string  `json:"pace"`
	} `json:"quota"`
	Today struct {
		SpentPct  float64 `json:"spent_pct"`
		BudgetPct float64 `json:"budget_pct"`
		LeftPct   float64 `json:"left_pct"`
	} `json:"today"`
}

// pace → (Okabe-Ito colorblind-safe hex, glyph, label)
func paceMeta(p string) (hex, glyph, label string) {
	switch p {
	case "on_track":
		return "#009E73", "✓", "on track"
	case "slightly_over":
		return "#E69F00", "▲", "slightly over"
	case "over_budget":
		return "#D55E00", "⯃", "over budget"
	}
	return "#8A8F99", "•", "—"
}

var (
	mVerdict, mToday, mBudget, mUsed, mReset *systray.MenuItem
)

func main() {
	addr := flag.String("addr", "127.0.0.1:7457", "weekstat daemon address")
	flag.Parse()
	statsURL = "http://" + *addr + "/stats"
	dashURL = "http://" + *addr + "/"
	systray.Run(onReady, func() {})
}

func onReady() {
	systray.SetTitle("wk")
	systray.SetTooltip("weekstat — weekly quota")
	systray.SetIcon(icon(0, "#8A8F99"))

	mVerdict = systray.AddMenuItem("connecting…", "current pace")
	mVerdict.Disable()
	systray.AddSeparator()
	mToday = systray.AddMenuItem("Today: —", "quota % burned so far today")
	mToday.Disable()
	mBudget = systray.AddMenuItem("Budget/day: —", "how much you may spend per remaining day")
	mBudget.Disable()
	mUsed = systray.AddMenuItem("Used: —", "used vs left this window")
	mUsed.Disable()
	mReset = systray.AddMenuItem("Resets: —", "time until the window resets")
	mReset.Disable()
	systray.AddSeparator()
	mOpen := systray.AddMenuItem("Open dashboard", "open the full dashboard in a browser")
	mRefresh := systray.AddMenuItem("Refresh now", "re-poll the daemon")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "quit the tray indicator")

	go func() {
		for {
			select {
			case <-mOpen.ClickedCh:
				_ = exec.Command("xdg-open", dashURL).Start()
			case <-mRefresh.ClickedCh:
				update()
			case <-mQuit.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()

	go func() {
		update()
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for range t.C {
			update()
		}
	}()
}

func fetch() (*stats, error) {
	resp, err := client.Get(statsURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var s stats
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return nil, err
	}
	return &s, nil
}

func update() {
	s, err := fetch()
	if err != nil {
		systray.SetTitle("wk ?")
		systray.SetTooltip("weekstat — daemon unreachable")
		systray.SetIcon(icon(0, "#8A8F99"))
		mVerdict.SetTitle("⚠ daemon unreachable")
		return
	}
	if !s.HasData {
		systray.SetTitle("wk …")
		systray.SetIcon(icon(0, "#8A8F99"))
		mVerdict.SetTitle("collecting data…")
		mToday.SetTitle("Today: —")
		mBudget.SetTitle("Budget/day: —")
		mUsed.SetTitle("Used: —")
		mReset.SetTitle("Resets: —")
		return
	}
	hex, glyph, label := paceMeta(s.Quota.Pace)
	systray.SetIcon(icon(s.Quota.UsedPct/100, hex))
	systray.SetTitle(fmt.Sprintf("%.0f%%", s.Quota.UsedPct))
	systray.SetTooltip(fmt.Sprintf("weekstat — %s · %.0f%% used, %.0f%% left",
		label, s.Quota.UsedPct, s.Quota.RemainingPct))

	mVerdict.SetTitle(fmt.Sprintf("%s  %s", glyph, label))
	mToday.SetTitle(fmt.Sprintf("Today: +%.1f%% of %.1f%%  ·  %.1f%% left", s.Today.SpentPct, s.Today.BudgetPct, s.Today.LeftPct))
	mBudget.SetTitle(fmt.Sprintf("Budget/day: %.1f%%/d  ·  %.1fd left", s.Quota.BudgetPerDayPct, s.Quota.DaysLeft))
	mUsed.SetTitle(fmt.Sprintf("Used %.0f%%  ·  left %.0f%%", s.Quota.UsedPct, s.Quota.RemainingPct))
	mReset.SetTitle(fmt.Sprintf("Resets in %.0f h", s.Window.ResetsInHours))
}

// icon renders a ring gauge PNG: a faint full track with a progress arc that
// sweeps clockwise from 12 o'clock for `frac` of the circle, in the pace color.
// It reads as a little "quota dial" — how much of the week is burned at a glance.
// Anti-aliased, transparent background, stdlib image/png only.
func icon(frac float64, hex string) []byte {
	frac = clamp01(frac)
	pc := parseHex(hex)
	const n = 64
	const outer, inner = 30.0, 18.0
	cx, cy := (n-1)/2.0, (n-1)/2.0
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	twoPi := 2 * math.Pi
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			dx, dy := float64(x)-cx, float64(y)-cy
			d := math.Hypot(dx, dy)
			// radial coverage with a 0.6px feather on each edge of the ring band
			cov := clamp01(d-inner+0.6) * clamp01(outer-d+0.6)
			if cov <= 0 {
				continue
			}
			theta := math.Atan2(dx, -dy) // 0 at top, increasing clockwise
			if theta < 0 {
				theta += twoPi
			}
			if theta <= frac*twoPi {
				img.SetNRGBA(x, y, color.NRGBA{pc.R, pc.G, pc.B, uint8(cov * 255)})
			} else {
				img.SetNRGBA(x, y, color.NRGBA{0xE5, 0xE7, 0xEB, uint8(cov * 64)}) // faint track
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

type rgb struct{ R, G, B uint8 }

func parseHex(s string) rgb {
	if len(s) == 7 && s[0] == '#' {
		var r, g, b int
		fmt.Sscanf(s[1:], "%02x%02x%02x", &r, &g, &b)
		return rgb{uint8(r), uint8(g), uint8(b)}
	}
	return rgb{0x8A, 0x8F, 0x99}
}
