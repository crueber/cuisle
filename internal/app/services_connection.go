package app

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/secrets"
	"github.com/crueber/cuisle/internal/source"
	syncpkg "github.com/crueber/cuisle/internal/sync"
	"github.com/crueber/cuisle/internal/view"
)

type ConnectionService struct{ d *Deps }

// Autocomplete result caps (jira-sync.md sections 2.3, 2.4, 2.12).
const (
	spaceLimit = 20
	boardLimit = 50
	userLimit  = 20
)

func (s *ConnectionService) Connection(ctx context.Context) (out Connection, err error) {
	defer s.d.guard("Connection", &err)
	return s.d.connection(ctx)
}

func (s *ConnectionService) Connect(ctx context.Context, in ConnectInput) (out Connection, err error) {
	defer s.d.guard("Connect", &err)
	// Connect verifies the token against the source BEFORE anything is stored, then keeps the secret in the keychain
	// only and the rest in the connection row.
	d := s.d
	kind, baseURL, err := validateConnectInput(in)
	if err != nil {
		return Connection{}, err
	}
	account := strings.TrimSpace(in.Account)
	// Refuse a switch to another site (while history exists) before any network call or keychain write; the
	// SaveConnection refusal below stays as the backstop for a race.
	if err := cache.CheckSiteSwitch(ctx, d.DB.R, baseURL); err != nil {
		return Connection{}, apperr.From(err)
	}
	conn := model.Connection{Source: kind, BaseURL: baseURL, Account: account}

	src, err := source.Open(conn, in.Secret, d.srcOpts(nil, false))
	if err != nil {
		return Connection{}, apperr.From(err)
	}
	acct, err := src.Test(ctx)
	if err != nil {
		if ctx.Err() == nil {
			d.noteNet(err)
		}
		if apperr.Is(err, apperr.AuthInvalid) {
			d.emit(syncpkg.EvConnectionAuth, syncpkg.ConnectionAuth{State: "invalid"})
		}
		return Connection{}, apperr.From(err)
	}
	d.noteNet(nil)

	prev, hadPrev, err := d.connRow(ctx)
	if err != nil {
		return Connection{}, err
	}
	keyAccount := secrets.ConnectionAccount(kind, baseURL, account)
	if err := d.Keyring.Set(keyAccount, in.Secret); err != nil {
		msg := "The keychain could not store the token."
		if errors.Is(err, secrets.ErrUnavailable) {
			msg = "The system keychain is not available, so the token cannot be stored."
		}
		return Connection{}, apperr.Wrap(apperr.KeychainUnavailable, msg, err)
	}

	now := d.now()
	row := cache.ConnRow{
		Source: kind, BaseURL: baseURL, Account: account, AccountID: acct.ID, DisplayName: acct.DisplayName,
		AuthState: "ok", VerifiedAt: now, DevActivityState: "unknown", CreatedAt: now,
	}
	if hadPrev {
		row.SourceMeta, row.DevActivityState, row.DevCheckedAt = prev.SourceMeta, prev.DevActivityState, prev.DevCheckedAt
		if !prev.CreatedAt.IsZero() {
			row.CreatedAt = prev.CreatedAt
		}
	}
	id, err := cache.SaveConnection(ctx, d.DB.W, row)
	if err != nil {
		// A refused Connect (for example a different site while history exists) leaves the keychain as it was: the
		// token written above is removed unless it is the existing connection's own item.
		if !hadPrev || keyAccount != secrets.ConnectionAccount(prev.Source, prev.BaseURL, prev.Account) {
			if derr := d.Keyring.Delete(keyAccount); derr != nil {
				d.log().Warn("could not remove the unused keychain item", "err", derr)
			}
		}
		return Connection{}, apperr.From(err)
	}
	if err := d.Settings.SetConnectionAll(ctx, id); err != nil {
		return Connection{}, err
	}
	if hadPrev {
		// A different account or site leaves the old token orphaned: remove it.
		if old := secrets.ConnectionAccount(prev.Source, prev.BaseURL, prev.Account); old != keyAccount {
			if err := d.Keyring.Delete(old); err != nil {
				d.log().Warn("could not remove the previous connection's keychain item", "err", err)
			}
		}
	}
	d.log().Info("connected", "source", kind, "site", baseURL)
	d.emit(syncpkg.EvConnectionAuth, syncpkg.ConnectionAuth{State: "ok"})
	return d.connection(ctx)
}

// validateConnectInput returns the source kind and the normalised base URL (no trailing slash). https is required
// except for loopback hosts (the fake Jira, local proxies).
func validateConnectInput(in ConnectInput) (kind, baseURL string, err error) {
	kind = in.Source
	if kind == "" {
		kind = "jira"
	}
	if !slices.Contains(source.Kinds(), kind) {
		return "", "", apperr.Validationf("source", "That source is not supported.")
	}
	raw := strings.TrimSpace(in.BaseURL)
	if raw == "" {
		return "", "", apperr.Validationf("baseUrl", "Enter your site address, for example https://yourteam.atlassian.net.")
	}
	u, perr := url.Parse(raw)
	if perr != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", "", apperr.Validationf("baseUrl", "Enter the full site address starting with https://.")
	}
	if u.User != nil {
		return "", "", apperr.Validationf("baseUrl", "Remove the user name and password from the site address.")
	}
	if u.Path != "" && u.Path != "/" {
		return "", "", apperr.Validationf("baseUrl", "Use the site address only, for example https://yourteam.atlassian.net.")
	}
	if u.Scheme == "http" && !isLoopback(u.Hostname()) {
		return "", "", apperr.Validationf("baseUrl", "Use an https:// address.")
	}
	if strings.TrimSpace(in.Account) == "" {
		return "", "", apperr.Validationf("account", "Enter the account you sign in with.")
	}
	if in.Secret == "" {
		return "", "", apperr.Validationf("secret", "Paste your API token.")
	}
	// Scheme and host are case-insensitive: lowercase them so https://Foo.atlassian.net equals https://foo.atlassian.net.
	u.Scheme, u.Host = strings.ToLower(u.Scheme), strings.ToLower(u.Host)
	u.Fragment, u.RawQuery = "", ""
	return kind, strings.TrimRight(u.String(), "/"), nil
}

// isLoopback matches the adapter's checkBaseURL: 127.0.0.1, ::1 and localhost only.
func isLoopback(host string) bool {
	return strings.EqualFold(host, "localhost") || host == "127.0.0.1" || host == "::1"
}

func (s *ConnectionService) Disconnect(ctx context.Context, deleteContent bool) (err error) {
	defer s.d.guard("Disconnect", &err)
	// Disconnect removes the keychain item. The connection row and every record and history row stay. deleteContent
	// (ux-spec §14.11, offered only past storage.cleanup_offer_mb) also deletes the synced content of the connection:
	// descriptions, comment bodies, field values, links, transition caches, drafts and the media cache; then VACUUM.
	d := s.d
	row, ok, err := d.connRow(ctx)
	if err != nil || !ok {
		return err
	}
	if err := d.Keyring.Delete(secrets.ConnectionAccount(row.Source, row.BaseURL, row.Account)); err != nil {
		return apperr.Wrap(apperr.KeychainUnavailable, "The keychain could not remove the token.", err)
	}
	d.setAuth(ctx, row, "missing")
	if deleteContent {
		if err := d.deleteContent(ctx, row.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *ConnectionService) SearchSpaces(ctx context.Context, query string) (out []view.SpaceOption, err error) {
	defer s.d.guard("SearchSpaces", &err)
	d := s.d
	out = []view.SpaceOption{}
	row, ok, err := d.connRow(ctx)
	if err != nil || !ok {
		return out, err
	}
	var spaces []model.Space
	used, err := d.live(ctx, row, func(src source.Source) error {
		found, err := src.SearchSpaces(ctx, query)
		if err != nil {
			return err
		}
		spaces = found
		d.cacheSpaces(ctx, row, found)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !used {
		if spaces, err = cache.SearchSpaces(ctx, d.DB.R, row.ID, query, spaceLimit); err != nil {
			return nil, err
		}
	}
	for _, sp := range spaces {
		out = append(out, view.SpaceOption{ID: sp.ID, Key: sp.Key, Name: sp.Name})
	}
	return out, nil
}

func (s *ConnectionService) SearchBoards(ctx context.Context, spaceKey string, query string) (out []view.BoardOption, err error) {
	defer s.d.guard("SearchBoards", &err)
	d := s.d
	out = []view.BoardOption{}
	row, ok, err := d.connRow(ctx)
	if err != nil || !ok {
		return out, err
	}
	var boards []model.Board
	used, err := d.live(ctx, row, func(src source.Source) error {
		found, err := src.SearchBoards(ctx, spaceKey, query)
		if err != nil {
			return err
		}
		boards = found
		d.cacheBoards(ctx, row, spaceKey, found)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !used {
		if boards, err = cache.SearchBoards(ctx, d.DB.R, row.ID, spaceKey, query, boardLimit); err != nil {
			return nil, err
		}
	}
	for _, b := range boards {
		out = append(out, view.BoardOption{ID: b.ID, Name: b.Name, Type: b.Kind})
	}
	return out, nil
}

func (s *ConnectionService) SearchUsers(ctx context.Context, standupID int64, query string) (out []UserOption, err error) {
	defer s.d.guard("SearchUsers", &err)
	d := s.d
	out = []UserOption{}
	st, err := d.loadLive(ctx, standupID)
	if err != nil {
		return nil, err
	}
	row, ok, err := d.connRow(ctx)
	if err != nil || !ok {
		return out, err
	}
	speakers, err := d.Settings.Speakers(ctx, standupID)
	if err != nil {
		return nil, err
	}
	onBoard := make(map[string]bool, len(speakers))
	for _, sp := range speakers {
		onBoard[sp.AccountID] = true
	}

	var people []model.Person
	switch {
	case query == "":
		// An empty query is the people the board already knows: no network.
		if people, err = d.boardPeople(ctx, row, standupID); err != nil {
			return nil, err
		}
	default:
		used := false
		if st.SpaceKey != "" {
			used, err = d.live(ctx, row, func(src source.Source) error {
				found, err := src.SearchUsers(ctx, st.SpaceKey, query)
				if err != nil {
					return err
				}
				people = found
				if err := cache.UpsertPeople(ctx, d.DB.W, row.ID, found, d.now()); err != nil {
					d.log().Warn("could not cache people", "err", err)
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
		}
		if !used {
			cached, err := cache.SearchPeople(ctx, d.DB.R, row.ID, query, userLimit)
			if err != nil {
				return nil, err
			}
			for _, p := range cached {
				if p.Active {
					people = append(people, p)
				}
			}
		}
	}
	for _, p := range people {
		out = append(out, UserOption{
			AccountID: p.AccountID, DisplayName: p.DisplayName, Initials: view.Initials(p.DisplayName), OnBoard: onBoard[p.AccountID],
		})
	}
	return out, nil
}

func (s *ConnectionService) ValidateFilter(ctx context.Context, standupID int64, expr string) (out view.FilterCheck, err error) {
	defer s.d.guard("ValidateFilter", &err)
	// ValidateFilter asks the source to parse the extra filter (an allowed network trigger) and records the result on
	// the standup. A filter the source rejects is a result (OK=false), not an error.
	d := s.d
	if _, err := d.loadLive(ctx, standupID); err != nil {
		return view.FilterCheck{}, err
	}
	ok, errs := true, []string{}
	if strings.TrimSpace(expr) != "" {
		src, row, err := d.requireSource(ctx, "Connect to Jira to check the filter.")
		if err != nil {
			return view.FilterCheck{}, err
		}
		okRes, msgs, err := src.ValidateFilter(ctx, expr)
		if err != nil {
			if ctx.Err() == nil {
				d.noteNet(err)
				d.noteSourceError(ctx, row, err)
			}
			return view.FilterCheck{}, apperr.From(err)
		}
		d.noteNet(nil)
		ok = okRes
		if msgs != nil {
			errs = msgs
		}
	}
	if err := d.Settings.SetFilterCheck(ctx, standupID, ok, errs); err != nil {
		return view.FilterCheck{}, err
	}
	return view.FilterCheck{OK: ok, Errors: errs, CheckedAt: d.now().UnixMilli()}, nil
}

// cacheSpaces keeps the autocomplete cache warm; failures only cost the offline fallback.
func (d *Deps) cacheSpaces(ctx context.Context, row cache.ConnRow, spaces []model.Space) {
	if err := cache.UpsertSpaces(ctx, d.DB.W, row.ID, spaces, d.now()); err != nil {
		d.log().Warn("could not cache spaces", "err", err)
	}
}

// cacheBoards files the boards under their space (the adapter does not know it) so the offline search can filter.
func (d *Deps) cacheBoards(ctx context.Context, row cache.ConnRow, spaceKey string, boards []model.Board) {
	spaceID := ""
	if spaceKey != "" {
		if spaces, err := cache.SearchSpaces(ctx, d.DB.R, row.ID, spaceKey, 0); err == nil {
			for _, sp := range spaces {
				if sp.Key == spaceKey {
					spaceID = sp.ID
				}
			}
		}
	}
	for i := range boards {
		if boards[i].SpaceID == "" {
			boards[i].SpaceID = spaceID
		}
	}
	if err := cache.UpsertBoards(ctx, d.DB.W, row.ID, boards, d.now()); err != nil {
		d.log().Warn("could not cache boards", "err", err)
	}
}
