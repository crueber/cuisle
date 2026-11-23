package app_test

// WP-38B: the read-only site-switch pre-check, case-insensitive site matching, the https-to-http address error found on
// the real stack, and the sink secret-bit cache that must not remember a failed keychain read.

import (
	"strings"
	"testing"

	"github.com/crueber/cuisle/internal/app"
	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/secrets"
	"github.com/crueber/cuisle/internal/sink"
	"github.com/crueber/cuisle/internal/source/jira/jiratest"
)

// The refused switch is decided before the token is tried: a wrong token against the other site still reports the
// conflict, with no request to the other site and no keychain write.
func TestConnect_OtherSiteWhileHistoryExists_IsRefusedBeforeAnyNetworkOrKeychainWork(t *testing.T) {
	ta := newTestApp(t)
	ta.scrumStandup()
	keysBefore := ta.KR.items()
	other := jiratest.NewServer(t, ta.Fx.fx, jiratest.Options{Now: baseTime, Loc: testLoc})

	in := ta.connectInput()
	in.BaseURL = other.URL
	in.Secret = "wrong-token"
	_, err := ta.Svc.Connection.Connect(ctx, in)
	if !apperr.Is(err, apperr.Conflict) {
		t.Fatalf("Connect = %v, want conflict (not auth_invalid)", err)
	}
	if reqs := other.Requests(); len(reqs) != 0 {
		t.Errorf("the other site saw %d requests, want 0: %v", len(reqs), reqs)
	}
	if got := ta.KR.items(); len(got) != len(keysBefore) {
		t.Errorf("keychain = %v, want unchanged %v", got, keysBefore)
	}
}

// Scheme and host compare case-insensitively: the same site typed differently is the same connection.
func TestConnect_SiteMatchIsCaseInsensitive(t *testing.T) {
	ta := newTestApp(t)
	ta.scrumStandup()
	before, err := cache.GetConnection(ctx, ta.DB.R)
	noErr(t, err, "GetConnection")

	in := ta.connectInput()
	in.BaseURL = "HTTP" + strings.TrimPrefix(ta.Fake.URL, "http")
	c, err := ta.Svc.Connection.Connect(ctx, in)
	noErr(t, err, "Connect with an upper-case scheme")
	if c.State != "ok" || c.BaseURL != ta.Fake.URL {
		t.Fatalf("connection = %+v, want ok at %s", c, ta.Fake.URL)
	}
	after, err := cache.GetConnection(ctx, ta.DB.R)
	noErr(t, err, "GetConnection")
	if after.ID != before.ID {
		t.Fatalf("connection id %d -> %d, want the same row", before.ID, after.ID)
	}
}

// Found on the real stack: the UI always sends https://, so a plain-http server answered "internal". It is an
// address problem the user can act on.
func TestConnect_HTTPSAddressOfAnHTTPServer_IsOfflineNotInternal(t *testing.T) {
	ta := newTestApp(t)
	in := ta.connectInput()
	in.BaseURL = "https" + strings.TrimPrefix(ta.Fake.URL, "http")
	_, err := ta.Svc.Connection.Connect(ctx, in)
	if !apperr.Is(err, apperr.Offline) {
		t.Fatalf("Connect = %v (%#v), want offline", err, err)
	}
}

// failingGetKeyring fails Get with a configurable error on top of a Keyring.
type failingGetKeyring struct {
	secrets.Keyring
	err error
}

func (f *failingGetKeyring) Get(account string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.Keyring.Get(account)
}

func TestSinkList_DoesNotCacheAFailedKeychainRead(t *testing.T) {
	sink.Register(needsSecretSink{})
	ta := newTestApp(t)
	secret := "s3cret"
	saved, err := ta.Svc.Sink.Save(ctx, app.SinkInput{
		Kind: needsSecretKind, Name: "Hook", Config: map[string]string{}, Secret: &secret, SendOn: "manual", Enabled: true,
	})
	noErr(t, err, "Save")

	ta.wire() // a restart: the bits are gone
	kr := &failingGetKeyring{Keyring: ta.KR, err: secrets.ErrUnavailable}
	ta.Deps.Keyring = kr
	hasSecret := func() bool {
		list, err := ta.Svc.Sink.List(ctx)
		noErr(t, err, "List")
		for _, sc := range list {
			if sc.ID == saved.ID {
				return sc.HasSecret
			}
		}
		t.Fatal("sink missing from List")
		return false
	}
	if hasSecret() {
		t.Fatal("HasSecret = true while the keychain is unavailable")
	}
	kr.err = nil // the keychain comes back: the failure must not have been remembered
	if !hasSecret() {
		t.Fatal("HasSecret still false after the keychain recovered (the failed read was cached)")
	}
}
