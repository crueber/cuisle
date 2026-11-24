package jira

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/source"
	"github.com/crueber/cuisle/internal/source/jira/jiratest"
	"github.com/crueber/cuisle/internal/source/jira/limiter"
)

// The core may not import the jira package, so it marks user-initiated calls with source.WithInteractive. The client
// must translate that into the limiter's interactive lane (F12, jira-sync §3.1).
func TestClient_SourceInteractiveMarkerUsesTheInteractiveLane(t *testing.T) {
	fx, err := jiratest.ParseFixture(jiratest.DefaultFixture)
	if err != nil {
		t.Fatal(err)
	}
	srv := jiratest.NewServer(t, fx, jiratest.Options{Now: time.Date(2026, 11, 23, 15, 0, 0, 0, time.UTC), Loc: time.UTC})
	conn := model.Connection{ID: 71, Source: Kind, BaseURL: srv.URL, Account: "dev@example.test"}
	pol, _ := newWP06Policy(t, PolicyOptions{})
	_ = DefaultPolicy()
	t.Cleanup(SetDefaultPolicy(pol))
	src, err := New(conn, jiratest.Token, source.Options{HTTP: &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}})
	if err != nil {
		t.Fatal(err)
	}
	var id string
	for _, is := range srv.Dataset().Issues {
		if is.ProjectKey == "MOB" {
			id = is.ID
			break
		}
	}
	before := LimiterFor(conn).Stats().Requests
	if _, err := src.GetIssue(source.WithInteractive(context.Background()), id); err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	after := LimiterFor(conn).Stats().Requests
	if after[limiter.Interactive] <= before[limiter.Interactive] {
		t.Fatalf("interactive permits %d → %d, want an increase", before[limiter.Interactive], after[limiter.Interactive])
	}
	if after[limiter.Sync] != before[limiter.Sync] {
		t.Errorf("sync permits %d → %d, want unchanged for a marked call", before[limiter.Sync], after[limiter.Sync])
	}
}
