package secrets

import (
	"errors"
	"testing"
)

type failingKeyring struct{ sets int }

func (f *failingKeyring) Get(string) (string, error) { return "", ErrUnavailable }
func (f *failingKeyring) Set(string, string) error   { f.sets++; return ErrUnavailable }
func (f *failingKeyring) Delete(string) error        { return ErrUnavailable }

func TestEnvTokenSetBypassWhenKeychainUnavailable(t *testing.T) {
	tests := []struct {
		name    string
		account string
		env     string
		secret  string
		wantErr bool
	}{
		{"jira env matches", "jira:acme.atlassian.net:me", "tok", "tok", false},
		{"jira env differs", "jira:acme.atlassian.net:me", "tok", "other", true},
		{"jira env empty", "jira:acme.atlassian.net:me", "", "tok", true},
		{"sink account not bypassed", "sink:1", "tok", "tok", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inner := &failingKeyring{}
			k := WithEnvToken(inner, func(string) string { return tc.env })
			err := k.Set(tc.account, tc.secret)
			if tc.wantErr {
				if !errors.Is(err, ErrUnavailable) {
					t.Fatalf("want ErrUnavailable, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("want nil, got %v", err)
			}
		})
	}
}
