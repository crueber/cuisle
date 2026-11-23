// Package secrets stores credentials in the OS keychain (service "crueber.cuisle"; architecture.md §2, §6.4).
// FROZEN CONTRACT: types and signatures. Implementation: WP-03.
package secrets

import (
	"errors"

	_ "github.com/zalando/go-keyring" // OS keychain backend for NewSystem
)

// Service is the keychain service name.
const Service = "crueber.cuisle"

// EnvJiraToken is the Linux-without-Secret-Service bypass (process env only, never persisted).
const EnvJiraToken = "CUISLE_JIRA_TOKEN"

var (
	ErrNotFound    = errors.New("secrets: not found")
	ErrUnavailable = errors.New("secrets: keychain unavailable")
)

// Keyring is implemented by the OS keychain and by an in-memory store (tests, --fake-jira).
type Keyring interface {
	Get(account string) (secret string, err error) // ErrNotFound when absent; ErrUnavailable when no keychain
	Set(account, secret string) error
	Delete(account string) error // deleting an absent item is not an error
}
