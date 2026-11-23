package secrets

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/zalando/go-keyring"
)

// ConnectionAccount returns "<source>:<host>:<account>", e.g. "jira:acme.atlassian.net:me@acme.com".
// baseURLOrHost may be a full URL ("https://acme.atlassian.net/") or a bare host; the host is lower-cased.
func ConnectionAccount(source, baseURLOrHost, account string) string {
	in := strings.TrimSpace(baseURLOrHost)
	if !strings.Contains(in, "://") {
		in = "//" + in // lets url.Parse read a bare host (with optional port) as Host
	}
	host := ""
	if u, err := url.Parse(in); err == nil {
		host = u.Host
	} else {
		host = strings.TrimPrefix(in, "//")
		if i := strings.Index(host, "/"); i >= 0 {
			host = host[:i]
		}
	}
	return source + ":" + strings.ToLower(host) + ":" + account
}

// SinkAccount returns "sink:<id>".
func SinkAccount(sinkID int64) string {
	return "sink:" + strconv.FormatInt(sinkID, 10)
}

// memory is an in-memory Keyring.
type memory struct {
	mu sync.Mutex
	m  map[string]string
}

// NewMemory returns an in-memory Keyring (safe for concurrent use).
func NewMemory() Keyring {
	return &memory{m: make(map[string]string)}
}

func (k *memory) Get(account string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.m[account]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (k *memory) Set(account, secret string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.m[account] = secret
	return nil
}

func (k *memory) Delete(account string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.m, account)
	return nil
}

// system is the OS keychain via go-keyring.
type system struct{}

// NewSystem returns the OS keychain via github.com/zalando/go-keyring under Service, mapping its not-found error to
// ErrNotFound and "no Secret Service" style failures to ErrUnavailable.
func NewSystem() Keyring {
	return system{}
}

func (system) Get(account string) (string, error) {
	v, err := keyring.Get(Service, account)
	switch {
	case errors.Is(err, keyring.ErrNotFound):
		return "", ErrNotFound
	case err != nil:
		return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return v, nil
}

func (system) Set(account, secret string) error {
	if err := keyring.Set(Service, account, secret); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return nil
}

func (system) Delete(account string) error {
	err := keyring.Delete(Service, account)
	switch {
	case err == nil, errors.Is(err, keyring.ErrNotFound):
		return nil
	default:
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
}

// envToken wraps a Keyring and answers jira accounts from the environment.
type envToken struct {
	inner  Keyring
	getenv func(string) string
}

// WithEnvToken wraps k so that Get of an account starting "jira:" returns os.Getenv(EnvJiraToken) when set.
func WithEnvToken(k Keyring, getenv func(string) string) Keyring {
	if getenv == nil {
		getenv = os.Getenv
	}
	return envToken{inner: k, getenv: getenv}
}

func (e envToken) Get(account string) (string, error) {
	if strings.HasPrefix(account, "jira:") {
		if tok := e.getenv(EnvJiraToken); tok != "" {
			return tok, nil
		}
	}
	return e.inner.Get(account)
}

// Set persists to the inner keyring. When there is no keychain (ErrUnavailable) and the secret is exactly the
// CUISLE_JIRA_TOKEN bypass token for a jira account, the env var is the store: nothing is persisted and Set succeeds.
func (e envToken) Set(account, secret string) error {
	err := e.inner.Set(account, secret)
	if err != nil && errors.Is(err, ErrUnavailable) && strings.HasPrefix(account, "jira:") {
		if tok := e.getenv(EnvJiraToken); tok != "" && tok == secret {
			return nil
		}
	}
	return err
}

func (e envToken) Delete(account string) error {
	return e.inner.Delete(account)
}
