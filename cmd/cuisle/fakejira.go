package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/store"

	"github.com/crueber/cuisle/internal/app"
	"github.com/crueber/cuisle/internal/source/jira"
	"github.com/crueber/cuisle/internal/source/jira/jiratest"
)

// fakeAccount is the sign-in the fake accepts with jiratest.Token (any e-mail works).
const fakeAccount = "dev@cuisle.test"

// startFakeJira starts the fake Jira on 127.0.0.1 over the embedded fixture or the fixture file at spec, with the
// fault injection of CUISLE_FAKE_FAULTS applied (jira-sync.md section 6).
//
// addr is the preferred listen address: fake mode reuses the port of the stored loopback connection so a relaunch is
// the "same site" (a different site is refused once synced data exists).
func startFakeJira(spec, addr string) (*jiratest.Server, *jiratest.Fixture, error) {
	raw := jiratest.DefaultFixture
	if spec != fakeDefault {
		b, err := os.ReadFile(spec)
		if err != nil {
			return nil, nil, fmt.Errorf("fake jira fixture: %w", err)
		}
		raw = b
	}
	fx, err := jiratest.ParseFixture(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("fake jira fixture: %w", err)
	}
	opts, err := jiratest.ParseFaults(os.Getenv("CUISLE_FAKE_FAULTS"), jiratest.Options{Now: time.Now(), Loc: time.Local, Addr: addr})
	if err != nil {
		return nil, nil, fmt.Errorf("CUISLE_FAKE_FAULTS: %w", err)
	}
	srv, err := jiratest.Start(fx, opts)
	if err != nil {
		return nil, nil, err
	}
	return srv, fx, nil
}

// seedFake pre-connects a site pointing at the fake (the port is new on every launch and the keyring is in memory,
// so this runs every time) and creates one standup per fixture board on first run, through the same service calls
// the UI makes. The first sync then happens through sync-on-open.
func seedFake(ctx context.Context, svcs *app.Services, srv *jiratest.Server, fx *jiratest.Fixture) error {
	if _, err := svcs.Connection.Connect(ctx, app.ConnectInput{
		Source: jira.Kind, BaseURL: srv.URL, Account: fakeAccount, Secret: jiratest.Token,
	}); err != nil {
		return fmt.Errorf("connect to the fake jira: %w", err)
	}
	tabs, err := svcs.Standup.List(ctx)
	if err != nil {
		return err
	}
	if len(tabs) > 0 {
		return nil
	}
	for _, b := range fx.Boards {
		if err := seedBoard(ctx, svcs, b); err != nil {
			return fmt.Errorf("standup for board %q: %w", b.Name, err)
		}
	}
	return nil
}

func seedBoard(ctx context.Context, svcs *app.Services, b jiratest.FxBoard) error {
	cfg, err := svcs.Config.Create(ctx)
	if err != nil {
		return err
	}
	spaces, err := svcs.Connection.SearchSpaces(ctx, b.ProjectKey)
	if err != nil {
		return err
	}
	spaceID, spaceName := "", b.Name
	for _, s := range spaces {
		if s.Key == b.ProjectKey {
			spaceID, spaceName = s.ID, s.Name
		}
	}
	if spaceID == "" {
		return fmt.Errorf("project %q not found", b.ProjectKey)
	}
	if _, err := svcs.Config.SetSpace(ctx, cfg.ID, spaceID); err != nil {
		return err
	}
	boards, err := svcs.Connection.SearchBoards(ctx, b.ProjectKey, "")
	if err != nil {
		return err
	}
	for _, bo := range boards {
		if bo.Name == b.Name {
			if _, err := svcs.Config.SetBoard(ctx, cfg.ID, bo.ID); err != nil {
				return err
			}
			name := spaceName // the project name ("Saffron Mobile"), as in the mockups
			_, err := svcs.Config.Update(ctx, cfg.ID, app.StandupPatch{Name: &name})
			return err
		}
	}
	return fmt.Errorf("board %q not found", b.Name)
}

// storedFakeAddr returns host:port of the stored connection when it points at a loopback http fake, else "".
func storedFakeAddr(ctx context.Context, db *store.DB) string {
	conn, err := cache.GetConnection(ctx, db.R)
	if err != nil {
		return ""
	}
	u, err := url.Parse(conn.BaseURL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" {
		return ""
	}
	return u.Host
}
