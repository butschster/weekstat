package main

import (
	"strings"
	"testing"
	"time"
)

// trimmed real payload from status.claude.com/api/v2/summary.json during the
// 2026-09-03 incident.
const summaryOutage = `{
 "page":{"id":"tymt9n04zgry","name":"Claude","url":"https://status.claude.com"},
 "components":[
  {"id":"rwppv331jlwc","name":"claude.ai","status":"partial_outage"},
  {"id":"0qbwn08sd68x","name":"Claude Console (platform.claude.com)","status":"operational"},
  {"id":"k8w3r06qmzrp","name":"Claude API (api.anthropic.com)","status":"partial_outage"},
  {"id":"yyzkbfz2thpt","name":"Claude Code","status":"partial_outage"}
 ],
 "incidents":[
  {"id":"x1","name":"Console login slow","status":"monitoring","shortlink":"https://stspg.io/other","started_at":"2026-09-03T12:00:00.000Z",
   "incident_updates":[{"body":"Monitoring.","affected_components":[{"code":"0qbwn08sd68x"}]}]},
  {"id":"461yvfrzpwtt","name":"Elevated errors for multiple models","status":"identified","shortlink":"https://stspg.io/9xz4hhmd1jzn","started_at":"2026-09-03T13:26:04.191Z",
   "incident_updates":[
    {"body":"We are continuing to work on a fix for this issue.","affected_components":[{"code":"rwppv331jlwc"},{"code":"k8w3r06qmzrp"},{"code":"yyzkbfz2thpt"}]},
    {"body":"We are investigating elevated errors.","affected_components":[{"code":"rwppv331jlwc"},{"code":"yyzkbfz2thpt"}]}
   ]}
 ]}`

const summaryOK = `{
 "page":{"url":"https://status.claude.com"},
 "components":[{"id":"yyzkbfz2thpt","name":"Claude Code","status":"operational"}],
 "incidents":[]}`

func TestParseStatusSummaryOutage(t *testing.T) {
	now := time.Date(2026, 9, 3, 15, 0, 0, 0, time.Local)
	ss, err := parseStatusSummary([]byte(summaryOutage), "Claude Code", now)
	if err != nil {
		t.Fatal(err)
	}
	if ss.Status != "partial_outage" || ss.Level != levelOutage || !ss.Outage {
		t.Errorf("status = %s level = %s outage = %v", ss.Status, ss.Level, ss.Outage)
	}
	if ss.Label != "partial outage" {
		t.Errorf("label = %q", ss.Label)
	}
	// picks the incident that names our component, not the unrelated one
	if ss.Incident != "Elevated errors for multiple models" {
		t.Errorf("incident = %q", ss.Incident)
	}
	if ss.IncidentStatus != "identified" || ss.IncidentURL != "https://stspg.io/9xz4hhmd1jzn" {
		t.Errorf("incident status/url = %q %q", ss.IncidentStatus, ss.IncidentURL)
	}
	if !strings.HasPrefix(ss.IncidentUpdate, "We are continuing") {
		t.Errorf("latest update not first: %q", ss.IncidentUpdate)
	}
	if ss.IncidentSince == "" || ss.CheckedAt == "" {
		t.Errorf("since/checked empty: %q %q", ss.IncidentSince, ss.CheckedAt)
	}
	if ss.PageURL != "https://status.claude.com" {
		t.Errorf("page url = %q", ss.PageURL)
	}
}

func TestParseStatusSummaryOperational(t *testing.T) {
	ss, err := parseStatusSummary([]byte(summaryOK), "claude code", time.Now()) // case-insensitive
	if err != nil {
		t.Fatal(err)
	}
	if ss.Level != levelOK || ss.Outage || ss.Incident != "" {
		t.Errorf("got %+v", ss)
	}
}

func TestParseStatusSummaryMissingComponent(t *testing.T) {
	if _, err := parseStatusSummary([]byte(summaryOK), "Claude Cowork", time.Now()); err == nil {
		t.Error("expected error for a component that is not on the page")
	}
	if _, err := parseStatusSummary([]byte("not json"), "Claude Code", time.Now()); err == nil {
		t.Error("expected error for garbage")
	}
}

func TestLevelOf(t *testing.T) {
	cases := map[string]string{
		"operational": levelOK, "degraded_performance": levelDegraded, "under_maintenance": levelDegraded,
		"partial_outage": levelOutage, "major_outage": levelOutage, "whatever": levelUnknown,
	}
	for in, want := range cases {
		if got, _ := levelOf(in); got != want {
			t.Errorf("levelOf(%s) = %s, want %s", in, got, want)
		}
	}
}

// A failed fetch must not fake an outage nor erase a known one.
func TestStatusCheckerKeepsLastGood(t *testing.T) {
	sc := newStatusChecker("http://127.0.0.1:1/nope", "Claude Code")
	sc.client.Timeout = 300 * time.Millisecond
	if sc.current().Level != levelUnknown {
		t.Fatalf("initial level = %s", sc.current().Level)
	}
	good, _ := parseStatusSummary([]byte(summaryOutage), "Claude Code", time.Now())
	sc.mu.Lock()
	sc.last = good
	sc.mu.Unlock()
	sc.checkOnce(time.Now())
	cur := sc.current()
	if !cur.Outage || cur.Error == "" {
		t.Errorf("after failed fetch: outage = %v error = %q", cur.Outage, cur.Error)
	}
}

func TestOutputCarriesService(t *testing.T) {
	a := newApp()
	if o := a.output(time.Now()); o.Service.Level != levelUnknown || o.Service.PageURL == "" {
		t.Errorf("no checker: %+v", o.Service)
	}
	a.status = newStatusChecker("", "Claude Code")
	good, _ := parseStatusSummary([]byte(summaryOutage), "Claude Code", time.Now())
	a.status.last = good
	if o := a.output(time.Now()); !o.Service.Outage {
		t.Errorf("with checker: %+v", o.Service)
	}
}
