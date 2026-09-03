// Service health of Claude Code, pulled from the public status page
// (https://status.claude.com — a Statuspage.io site). The daemon polls the
// summary endpoint on its own, slower (5 min), schedule and publishes the result as
// the "service" block of /stats and week-stats.json, so the statusline, the
// tray and the dashboard can swap the usage figures for an "API is down"
// badge while an outage is on — usage numbers are meaningless when requests
// don't go through anyway.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	defaultStatusURL       = "https://status.claude.com/api/v2/summary.json"
	defaultStatusPage      = "https://status.claude.com"
	defaultStatusComponent = "Claude Code"
)

// Status levels, folded from Statuspage's component statuses.
const (
	levelOK       = "ok"       // operational
	levelDegraded = "degraded" // degraded_performance | under_maintenance — works, but expect trouble
	levelOutage   = "outage"   // partial_outage | major_outage — consumers hide usage, show the badge
	levelUnknown  = "unknown"  // never fetched successfully
)

// ServiceStatus is the public shape of the health check.
type ServiceStatus struct {
	Component string `json:"component"` // status-page component we track ("Claude Code")
	Status    string `json:"status"`    // raw Statuspage status: operational | degraded_performance | partial_outage | major_outage | under_maintenance | unknown
	Level     string `json:"level"`     // ok | degraded | outage | unknown
	Outage    bool   `json:"outage"`    // level == outage: show the "API down" badge instead of usage
	Label     string `json:"label"`     // human wording of Status ("partial outage")

	Incident       string `json:"incident,omitempty"`        // title of the open incident affecting the component
	IncidentStatus string `json:"incident_status,omitempty"` // investigating | identified | monitoring
	IncidentURL    string `json:"incident_url,omitempty"`
	IncidentSince  string `json:"incident_since,omitempty"`  // local time the incident started
	IncidentUpdate string `json:"incident_update,omitempty"` // latest update body

	PageURL   string `json:"page_url"`             // where to look for details
	CheckedAt string `json:"checked_at,omitempty"` // last SUCCESSFUL fetch (local time)
	Error     string `json:"error,omitempty"`      // last fetch error; Status is then the last known good value
}

// levelOf folds a Statuspage component status into our level + label.
func levelOf(status string) (level, label string) {
	switch status {
	case "operational":
		return levelOK, "operational"
	case "degraded_performance":
		return levelDegraded, "degraded performance"
	case "under_maintenance":
		return levelDegraded, "under maintenance"
	case "partial_outage":
		return levelOutage, "partial outage"
	case "major_outage":
		return levelOutage, "major outage"
	}
	return levelUnknown, "unknown"
}

// statuspageSummary is the subset of /api/v2/summary.json we read.
type statuspageSummary struct {
	Page struct {
		URL string `json:"url"`
	} `json:"page"`
	Components []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"components"`
	Incidents []struct {
		Name      string `json:"name"`
		Status    string `json:"status"`
		Shortlink string `json:"shortlink"`
		StartedAt string `json:"started_at"`
		Updates   []struct {
			Body               string `json:"body"`
			AffectedComponents []struct {
				Code string `json:"code"`
			} `json:"affected_components"`
		} `json:"incident_updates"`
	} `json:"incidents"`
}

// parseStatusSummary extracts the component's status and the open incident
// that names it (Statuspage's summary lists unresolved incidents only).
func parseStatusSummary(b []byte, component string, now time.Time) (ServiceStatus, error) {
	var sum statuspageSummary
	if err := json.Unmarshal(b, &sum); err != nil {
		return ServiceStatus{}, fmt.Errorf("decode summary: %w", err)
	}
	ss := ServiceStatus{Component: component, PageURL: defaultStatusPage}
	if sum.Page.URL != "" {
		ss.PageURL = sum.Page.URL
	}
	compID := ""
	for _, c := range sum.Components {
		if strings.EqualFold(strings.TrimSpace(c.Name), component) {
			compID, ss.Status = c.ID, c.Status
			break
		}
	}
	if compID == "" {
		return ServiceStatus{}, fmt.Errorf("component %q not on the status page", component)
	}
	ss.Level, ss.Label = levelOf(ss.Status)
	ss.Outage = ss.Level == levelOutage
	ss.CheckedAt = now.Local().Format("2006-01-02 15:04:05")

	// The incident that touches our component — newest first in the feed.
	for _, inc := range sum.Incidents {
		hit := false
		for _, u := range inc.Updates {
			for _, ac := range u.AffectedComponents {
				if ac.Code == compID {
					hit = true
				}
			}
		}
		if !hit {
			continue
		}
		ss.Incident = inc.Name
		ss.IncidentStatus = inc.Status
		ss.IncidentURL = inc.Shortlink
		if t, err := time.Parse(time.RFC3339Nano, inc.StartedAt); err == nil {
			ss.IncidentSince = t.Local().Format("2006-01-02 15:04")
		}
		if len(inc.Updates) > 0 { // updates are newest first
			ss.IncidentUpdate = strings.TrimSpace(inc.Updates[0].Body)
		}
		break
	}
	return ss, nil
}

// statusChecker polls the status page and keeps the last known result.
type statusChecker struct {
	url       string
	component string
	client    *http.Client

	mu   sync.RWMutex
	last ServiceStatus
}

func newStatusChecker(url, component string) *statusChecker {
	sc := &statusChecker{url: url, component: component, client: &http.Client{Timeout: 10 * time.Second}}
	sc.last = ServiceStatus{Component: component, Status: "unknown", Level: levelUnknown, Label: "unknown", PageURL: defaultStatusPage}
	return sc
}

func (sc *statusChecker) current() ServiceStatus {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.last
}

func (sc *statusChecker) fetch(now time.Time) (ServiceStatus, error) {
	req, err := http.NewRequest(http.MethodGet, sc.url, nil)
	if err != nil {
		return ServiceStatus{}, err
	}
	req.Header.Set("User-Agent", "weekstat/"+version)
	resp, err := sc.client.Do(req)
	if err != nil {
		return ServiceStatus{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ServiceStatus{}, fmt.Errorf("status page: HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return ServiceStatus{}, err
	}
	return parseStatusSummary(b, sc.component, now)
}

// checkOnce refreshes the cached status. On failure the last good status is
// kept (with Error set) so a flaky network never fakes an outage — or hides
// one that was already known.
func (sc *statusChecker) checkOnce(now time.Time) {
	ss, err := sc.fetch(now)
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if err != nil {
		sc.last.Error = err.Error()
		return
	}
	if sc.last.Level != ss.Level || sc.last.Incident != ss.Incident {
		log.Printf("service status: %s is %s (%s) %s", ss.Component, ss.Label, ss.Status, ss.Incident)
	}
	sc.last = ss
}

func (sc *statusChecker) loop(interval time.Duration) {
	sc.checkOnce(time.Now())
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for range tick.C {
		sc.checkOnce(time.Now())
	}
}
