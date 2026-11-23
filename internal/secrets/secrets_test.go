package secrets_test

// SPEC-NOTE: ConnectionAccount keeps the port when the input carries one (url.URL.Host, not Hostname), so two fake
// Jira servers on 127.0.0.1:<port> get different keychain accounts. A bare host with a port ("127.0.0.1:8080") must
// work too. Only the host is lower-cased; the account part (e.g. an email) keeps its case. Any URL path is dropped.
// SPEC-NOTE: WithEnvToken returns the env token for a "jira:" account even when the wrapped Keyring would return
// ErrNotFound or ErrUnavailable (that is the purpose of the bypass). Set and Delete always pass through to the
// wrapped Keyring; the env token is never written to it. An empty env value means "not set".
// SPEC-NOTE: Setting an empty secret is not tested (unspecified).

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/crueber/cuisle/internal/secrets"
)

// ---------------------------------------------------------------- accounts

func TestConnectionAccount(t *testing.T) {
	tests := []struct {
		name, source, base, account, want string
	}{
		{"url with trailing slash and mixed case", "jira", "https://Acme.atlassian.net/", "me@acme.com", "jira:acme.atlassian.net:me@acme.com"},
		{"url without trailing slash", "jira", "https://acme.atlassian.net", "me@acme.com", "jira:acme.atlassian.net:me@acme.com"},
		{"url with path", "jira", "https://acme.atlassian.net/jira/software", "me@acme.com", "jira:acme.atlassian.net:me@acme.com"},
		{"bare host", "jira", "acme.atlassian.net", "me@acme.com", "jira:acme.atlassian.net:me@acme.com"},
		{"bare host mixed case", "jira", "Acme.Atlassian.NET", "me@acme.com", "jira:acme.atlassian.net:me@acme.com"},
		{"account case is kept", "jira", "https://acme.atlassian.net", "Me@Acme.com", "jira:acme.atlassian.net:Me@Acme.com"},
		{"url with port (fake jira)", "jira", "http://127.0.0.1:8080", "me@acme.com", "jira:127.0.0.1:8080:me@acme.com"},
		{"bare host with port", "jira", "127.0.0.1:8080", "me@acme.com", "jira:127.0.0.1:8080:me@acme.com"},
		{"other source kind", "linear", "https://API.linear.app/", "bot", "linear:api.linear.app:bot"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := secrets.ConnectionAccount(tc.source, tc.base, tc.account); got != tc.want {
				t.Errorf("ConnectionAccount(%q, %q, %q) = %q, want %q", tc.source, tc.base, tc.account, got, tc.want)
			}
		})
	}
}

func TestSinkAccount(t *testing.T) {
	tests := []struct {
		id   int64
		want string
	}{
		{7, "sink:7"},
		{1, "sink:1"},
		{0, "sink:0"},
		{9223372036854775807, "sink:9223372036854775807"},
	}
	for _, tc := range tests {
		if got := secrets.SinkAccount(tc.id); got != tc.want {
			t.Errorf("SinkAccount(%d) = %q, want %q", tc.id, got, tc.want)
		}
	}
}

func TestConstants(t *testing.T) {
	if secrets.Service != "crueber.cuisle" {
		t.Errorf("Service = %q", secrets.Service)
	}
	if secrets.EnvJiraToken != "CUISLE_JIRA_TOKEN" {
		t.Errorf("EnvJiraToken = %q", secrets.EnvJiraToken)
	}
	if errors.Is(secrets.ErrNotFound, secrets.ErrUnavailable) || errors.Is(secrets.ErrUnavailable, secrets.ErrNotFound) {
		t.Error("ErrNotFound and ErrUnavailable must be distinct")
	}
}

// ---------------------------------------------------------------- memory keyring

func TestMemory_GetAbsentIsErrNotFound(t *testing.T) {
	k := secrets.NewMemory()
	got, err := k.Get("jira:acme.atlassian.net:me@acme.com")
	if !errors.Is(err, secrets.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if got != "" {
		t.Errorf("secret = %q, want empty", got)
	}
}

func TestMemory_SetThenGet(t *testing.T) {
	k := secrets.NewMemory()
	if err := k.Set("sink:7", "s3cret"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := k.Get("sink:7")
	if err != nil || got != "s3cret" {
		t.Errorf("Get = %q, %v", got, err)
	}
}

func TestMemory_SetOverwrites(t *testing.T) {
	k := secrets.NewMemory()
	k.Set("a", "one")
	if err := k.Set("a", "two"); err != nil {
		t.Fatal(err)
	}
	if got, err := k.Get("a"); err != nil || got != "two" {
		t.Errorf("Get = %q, %v, want two", got, err)
	}
}

func TestMemory_AccountsAreIsolated(t *testing.T) {
	k := secrets.NewMemory()
	k.Set("a", "A")
	k.Set("b", "B")
	k.Delete("a")
	if _, err := k.Get("a"); !errors.Is(err, secrets.ErrNotFound) {
		t.Errorf("a: err = %v", err)
	}
	if got, err := k.Get("b"); err != nil || got != "B" {
		t.Errorf("b = %q, %v", got, err)
	}
}

func TestMemory_DeleteRemoves(t *testing.T) {
	k := secrets.NewMemory()
	k.Set("a", "A")
	if err := k.Delete("a"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := k.Get("a"); !errors.Is(err, secrets.ErrNotFound) {
		t.Errorf("after Delete: err = %v, want ErrNotFound", err)
	}
}

func TestMemory_DeleteAbsentIsNotAnError(t *testing.T) {
	k := secrets.NewMemory()
	if err := k.Delete("never-set"); err != nil {
		t.Errorf("Delete absent = %v, want nil", err)
	}
	k.Set("a", "A")
	k.Delete("a")
	if err := k.Delete("a"); err != nil {
		t.Errorf("second Delete = %v, want nil", err)
	}
}

func TestMemory_InstancesAreIndependent(t *testing.T) {
	k1, k2 := secrets.NewMemory(), secrets.NewMemory()
	k1.Set("a", "A")
	if _, err := k2.Get("a"); !errors.Is(err, secrets.ErrNotFound) {
		t.Errorf("k2 saw k1's secret: err = %v", err)
	}
}

func TestMemory_ConcurrentUse(t *testing.T) {
	k := secrets.NewMemory()
	const workers = 32
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			acct := fmt.Sprintf("sink:%d", i)
			want := fmt.Sprintf("secret-%d", i)
			for r := 0; r < 100; r++ {
				if err := k.Set(acct, want); err != nil {
					t.Errorf("Set: %v", err)
					return
				}
				if got, err := k.Get(acct); err != nil || got != want {
					t.Errorf("Get(%s) = %q, %v, want %q", acct, got, err, want)
					return
				}
				// Contended account: any outcome except a panic or a data race is fine.
				k.Set("shared", want)
				k.Get("shared")
				k.Delete("shared")
			}
		}(i)
	}
	wg.Wait()
	for i := 0; i < workers; i++ {
		acct := fmt.Sprintf("sink:%d", i)
		if got, err := k.Get(acct); err != nil || got != fmt.Sprintf("secret-%d", i) {
			t.Errorf("final Get(%s) = %q, %v", acct, got, err)
		}
	}
}

// ---------------------------------------------------------------- system keyring

func TestNewSystem_ReturnsAKeyringWithoutTouchingTheKeychain(t *testing.T) {
	var k secrets.Keyring = secrets.NewSystem()
	if k == nil {
		t.Fatal("NewSystem returned nil")
	}
}

// ---------------------------------------------------------------- WithEnvToken

// failingKeyring simulates Linux without a Secret Service provider.
type failingKeyring struct{}

func (failingKeyring) Get(string) (string, error) { return "", secrets.ErrUnavailable }
func (failingKeyring) Set(string, string) error   { return secrets.ErrUnavailable }
func (failingKeyring) Delete(string) error        { return secrets.ErrUnavailable }

// envOf returns a getenv that answers only for CUISLE_JIRA_TOKEN and records the keys it was asked for.
func envOf(token string, asked *[]string) func(string) string {
	return func(k string) string {
		if asked != nil {
			*asked = append(*asked, k)
		}
		if k == secrets.EnvJiraToken {
			return token
		}
		return ""
	}
}

const jiraAcct = "jira:acme.atlassian.net:me@acme.com"

func TestWithEnvToken_JiraAccountReturnsEnvTokenOverStoredSecret(t *testing.T) {
	inner := secrets.NewMemory()
	inner.Set(jiraAcct, "stored")
	k := secrets.WithEnvToken(inner, envOf("env-token", nil))
	got, err := k.Get(jiraAcct)
	if err != nil || got != "env-token" {
		t.Errorf("Get = %q, %v, want env-token", got, err)
	}
}

func TestWithEnvToken_JiraAccountWorksWhenNothingIsStored(t *testing.T) {
	k := secrets.WithEnvToken(secrets.NewMemory(), envOf("env-token", nil))
	got, err := k.Get(jiraAcct)
	if err != nil || got != "env-token" {
		t.Errorf("Get = %q, %v, want env-token", got, err)
	}
}

func TestWithEnvToken_JiraAccountWorksWhenKeychainIsUnavailable(t *testing.T) {
	k := secrets.WithEnvToken(failingKeyring{}, envOf("env-token", nil))
	got, err := k.Get(jiraAcct)
	if err != nil || got != "env-token" {
		t.Errorf("Get = %q, %v, want env-token and no error", got, err)
	}
}

func TestWithEnvToken_FallsBackToWrappedKeyringWhenEnvEmpty(t *testing.T) {
	inner := secrets.NewMemory()
	k := secrets.WithEnvToken(inner, envOf("", nil))

	if _, err := k.Get(jiraAcct); !errors.Is(err, secrets.ErrNotFound) {
		t.Errorf("absent: err = %v, want ErrNotFound", err)
	}
	inner.Set(jiraAcct, "stored")
	if got, err := k.Get(jiraAcct); err != nil || got != "stored" {
		t.Errorf("stored: Get = %q, %v", got, err)
	}
}

func TestWithEnvToken_FallbackPropagatesUnavailable(t *testing.T) {
	k := secrets.WithEnvToken(failingKeyring{}, envOf("", nil))
	if _, err := k.Get(jiraAcct); !errors.Is(err, secrets.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestWithEnvToken_NonJiraAccountsNeverGetTheEnvToken(t *testing.T) {
	inner := secrets.NewMemory()
	inner.Set("sink:7", "sink-secret")
	inner.Set("jira", "no-colon-account")
	inner.Set("jiraffe:h:u", "other-prefix")
	k := secrets.WithEnvToken(inner, envOf("env-token", nil))

	tests := []struct {
		account string
		want    string
		wantErr error
	}{
		{"sink:7", "sink-secret", nil},
		{"sink:8", "", secrets.ErrNotFound},
		{"sink:jira:acme", "", secrets.ErrNotFound},
		{"jira", "no-colon-account", nil},
		{"jiraffe:h:u", "other-prefix", nil},
		{"linear:api.linear.app:bot", "", secrets.ErrNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.account, func(t *testing.T) {
			got, err := k.Get(tc.account)
			if got != tc.want || !errors.Is(err, tc.wantErr) {
				t.Errorf("Get(%q) = %q, %v; want %q, %v", tc.account, got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestWithEnvToken_NonJiraAccountPropagatesUnavailable(t *testing.T) {
	k := secrets.WithEnvToken(failingKeyring{}, envOf("env-token", nil))
	if _, err := k.Get("sink:7"); !errors.Is(err, secrets.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestWithEnvToken_ReadsOnlyTheEnvJiraTokenVariable(t *testing.T) {
	var asked []string
	k := secrets.WithEnvToken(secrets.NewMemory(), envOf("env-token", &asked))
	k.Get(jiraAcct)
	if len(asked) == 0 {
		t.Fatal("getenv was never consulted for a jira account")
	}
	for _, key := range asked {
		if key != secrets.EnvJiraToken {
			t.Errorf("getenv called with %q, want only %q", key, secrets.EnvJiraToken)
		}
	}
}

func TestWithEnvToken_SetAndDeletePassThroughAndNeverPersistTheEnvToken(t *testing.T) {
	inner := secrets.NewMemory()
	k := secrets.WithEnvToken(inner, envOf("env-token", nil))

	if err := k.Set(jiraAcct, "stored"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got, err := inner.Get(jiraAcct); err != nil || got != "stored" {
		t.Errorf("inner after Set = %q, %v, want stored", got, err)
	}

	if err := k.Delete(jiraAcct); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := inner.Get(jiraAcct); !errors.Is(err, secrets.ErrNotFound) {
		t.Errorf("inner after Delete: err = %v, want ErrNotFound (env token must not have been persisted)", err)
	}

	// Reading through the wrapper must not write the env token into the wrapped store.
	k.Get(jiraAcct)
	if _, err := inner.Get(jiraAcct); !errors.Is(err, secrets.ErrNotFound) {
		t.Errorf("Get persisted the env token: err = %v", err)
	}
}

func TestWithEnvToken_SetPropagatesWrappedErrors(t *testing.T) {
	k := secrets.WithEnvToken(failingKeyring{}, envOf("env-token", nil))
	if err := k.Set(jiraAcct, "x"); !errors.Is(err, secrets.ErrUnavailable) {
		t.Errorf("Set err = %v, want ErrUnavailable", err)
	}
}
