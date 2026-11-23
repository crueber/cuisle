package app_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/crueber/cuisle/internal/app"
	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/secrets"
	"github.com/crueber/cuisle/internal/sink"
)

// countingKeyring counts Get calls on top of a Keyring.
type countingKeyring struct {
	secrets.Keyring
	gets atomic.Int32
}

func (c *countingKeyring) Get(account string) (string, error) {
	c.gets.Add(1)
	return c.Keyring.Get(account)
}

// needsSecretSink is a sink kind whose capabilities say it needs a secret.
type needsSecretSink struct{}

const needsSecretKind = "review-fix2-needs-secret"

func (needsSecretSink) Kind() string        { return needsSecretKind }
func (needsSecretSink) DisplayName() string { return "Needs secret" }
func (needsSecretSink) Capabilities() sink.Capabilities {
	return sink.Capabilities{Current: true, Formats: []string{"json"}, NeedsSecret: true}
}
func (needsSecretSink) ConfigSchema() []sink.Field { return []sink.Field{} }
func (needsSecretSink) Validate(map[string]string) error {
	return nil
}
func (needsSecretSink) Deliver(context.Context, sink.Delivery) (sink.Receipt, error) {
	return sink.Receipt{}, apperr.New(apperr.SinkFailed, "not used")
}

func TestReviewFix2_SinkListDoesNotReadTheKeychainEveryCall(t *testing.T) {
	sink.Register(needsSecretSink{})
	ta := newTestApp(t)
	kr := &countingKeyring{Keyring: ta.KR}
	ta.Deps.Keyring = kr
	secret := "s3cret"

	file, err := ta.Svc.Sink.Save(ctx, app.SinkInput{
		Kind: "file", Name: "Folder", Config: map[string]string{"dir": ta.SinkDir}, SendOn: "manual", Enabled: true,
	})
	noErr(t, err, "Save file sink")
	withSecret, err := ta.Svc.Sink.Save(ctx, app.SinkInput{
		Kind: needsSecretKind, Name: "Hook", Config: map[string]string{}, Secret: &secret, SendOn: "manual", Enabled: true,
	})
	noErr(t, err, "Save secret sink")
	if !withSecret.HasSecret {
		t.Fatal("HasSecret = false after saving a secret")
	}

	kr.gets.Store(0)
	for i := 0; i < 3; i++ {
		list, err := ta.Svc.Sink.List(ctx)
		noErr(t, err, "List")
		for _, sc := range list {
			if sc.ID == withSecret.ID && !sc.HasSecret {
				t.Fatal("List lost HasSecret")
			}
			if sc.ID == file.ID && sc.HasSecret {
				t.Fatal("file sink has no secret")
			}
		}
	}
	if got := kr.gets.Load(); got != 0 {
		t.Fatalf("List read the keychain %d times, want 0 (bits cached by Save)", got)
	}

	// A restart loses the in-process bits: the first List reads the keychain once, only for the NeedsSecret sink.
	ta.wire()
	kr2 := &countingKeyring{Keyring: ta.KR}
	ta.Deps.Keyring = kr2
	for i := 0; i < 3; i++ {
		list, err := ta.Svc.Sink.List(ctx)
		noErr(t, err, "List after restart")
		for _, sc := range list {
			if sc.ID == withSecret.ID && !sc.HasSecret {
				t.Fatal("HasSecret lost after restart")
			}
		}
	}
	if got := kr2.gets.Load(); got != 1 {
		t.Fatalf("keychain reads after restart = %d, want 1", got)
	}

	// Removing the secret updates the bit without another read.
	empty := ""
	kr2.gets.Store(0)
	sc, err := ta.Svc.Sink.Save(ctx, app.SinkInput{
		ID: withSecret.ID, Kind: needsSecretKind, Name: "Hook", Config: map[string]string{}, Secret: &empty,
		SendOn: "manual", Enabled: true,
	})
	noErr(t, err, "Save clearing the secret")
	if sc.HasSecret {
		t.Fatal("HasSecret still true after clearing")
	}
	list, err := ta.Svc.Sink.List(ctx)
	noErr(t, err, "List")
	for _, it := range list {
		if it.ID == withSecret.ID && it.HasSecret {
			t.Fatal("List reports a deleted secret")
		}
	}
	if got := kr2.gets.Load(); got != 0 {
		t.Fatalf("keychain reads = %d, want 0", got)
	}
}
