package jiratest

// The F12 fault handlers (jira-sync.md §6 "Fault injection", WP-T6): Options.RefreshFails and Options.StaleChangelog.
//
//   - RefreshFails: GET /rest/api/3/issue/{id} answers 500 for an issue that had a successful write since the server
//     started (the "refresh_failed" path of a light write).
//   - StaleChangelog: a write applies as usual, but POST changelog/bulkfetch omits the changelog entries that write
//     created until the next Mutate (Jira's changelog lagging behind a write).
//
// The frozen Server struct has no slot for this state, so it lives in a package-level table keyed by *Server and is
// dropped by Close. Every function here runs with Server.mu held (handlers do), except forget, which takes it itself.

import (
	"net/http"
	"strings"
	"sync"
)

type f12State struct {
	written map[string]bool // issue ids with a successful write (RefreshFails)
	hidden  map[string]bool // changelog entry ids bulkfetch omits (StaleChangelog)
}

var f12 = struct {
	mu sync.Mutex
	m  map[*Server]*f12State
}{m: map[*Server]*f12State{}}

func (s *Server) f12State(create bool) *f12State {
	f12.mu.Lock()
	defer f12.mu.Unlock()
	st := f12.m[s]
	if st == nil && create {
		st = &f12State{written: map[string]bool{}, hidden: map[string]bool{}}
		f12.m[s] = st
	}
	return st
}

// f12Forget drops the server's fault state (Close).
func (s *Server) f12Forget() {
	f12.mu.Lock()
	delete(f12.m, s)
	f12.mu.Unlock()
}

// f12WriteTarget returns the issue a light-write request addresses, or nil when the request is not a write.
func (s *Server) f12WriteTarget(r *http.Request) *DSIssue {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		return nil
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/rest/api/3/issue/")
	if !ok {
		return nil
	}
	ref, _, _ := strings.Cut(rest, "/")
	if ref == "" {
		return nil
	}
	return s.issueByRef(ref)
}

// f12Before notes how many changelog entries the written issue has, so f12After can tell which ones the write added.
func (s *Server) f12Before(r *http.Request) (is *DSIssue, entries int) {
	if !s.opts.RefreshFails && !s.opts.StaleChangelog {
		return nil, 0
	}
	is = s.f12WriteTarget(r)
	if is == nil {
		return nil, 0
	}
	return is, len(is.Changelog)
}

// f12After arms the faults after a write that succeeded (a 2xx answer).
func (s *Server) f12After(is *DSIssue, entries int, status int) {
	if is == nil || status < 200 || status > 299 {
		return
	}
	st := s.f12State(true)
	if s.opts.RefreshFails {
		st.written[is.ID] = true
	}
	if s.opts.StaleChangelog && len(is.Changelog) > entries {
		for _, ch := range is.Changelog[entries:] {
			st.hidden[ch.ID] = true
		}
	}
}

// f12RefreshFails reports whether GET /issue/{id} must answer 500 for is.
func (s *Server) f12RefreshFails(is *DSIssue) bool {
	if !s.opts.RefreshFails || is == nil {
		return false
	}
	st := s.f12State(false)
	return st != nil && st.written[is.ID]
}

// f12Stale reports whether bulkfetch must omit the changelog entry.
func (s *Server) f12Stale(id string) bool {
	if !s.opts.StaleChangelog {
		return false
	}
	st := s.f12State(false)
	return st != nil && st.hidden[id]
}

// f12Mutated serves the lagging entries again: Mutate is "the next moment the dataset is touched".
func (s *Server) f12Mutated() {
	if st := s.f12State(false); st != nil {
		st.hidden = map[string]bool{}
	}
}
