package jiratest

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// NewServer starts a fake on 127.0.0.1:0 for tb and closes it in tb.Cleanup.
func NewServer(tb testing.TB, fx *Fixture, opts Options) *Server {
	tb.Helper()
	s, err := Start(fx, opts)
	if err != nil {
		tb.Fatalf("jiratest: %v", err)
	}
	tb.Cleanup(s.Close)
	return s
}

// Start starts a fake without a testing.TB (used by cuisle --fake-jira). Close it with Close.
func Start(fx *Fixture, opts Options) (*Server, error) {
	if fx == nil {
		fx = &Fixture{}
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	if opts.Loc == nil {
		opts.Loc = time.Local
	}
	addr := opts.Addr
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil && addr != "127.0.0.1:0" {
		ln, err = net.Listen("tcp", "127.0.0.1:0") // preferred port taken: fall back to a random one
	}
	if err != nil {
		return nil, fmt.Errorf("jiratest: listen: %w", err)
	}
	s := &Server{
		URL:    "http://" + ln.Addr().String(),
		ds:     Materialize(fx, opts.Now, opts.Loc),
		opts:   opts,
		failed: map[string]int{},
	}
	s.srv = &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

// Close stops the server.
func (s *Server) Close() {
	if s.srv != nil {
		_ = s.srv.Close()
	}
}

// Requests returns a copy of the request log. ResetRequests clears it.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.log...)
}

func (s *Server) ResetRequests() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = nil
}

// Mutate runs fn on the dataset under the lock (between syncs: move an issue, add a commit).
func (s *Server) Mutate(fn func(ds *Dataset)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s.ds)
}

// SetUnauthorized makes every following request answer 401 (true) or work normally (false).
func (s *Server) SetUnauthorized(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unauthorized = v
}

// Dataset returns the live dataset pointer (read it under Mutate when the server is in use).
func (s *Server) Dataset() *Dataset {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ds
}

// ServeHTTP implements http.Handler. The response is buffered so that the request log entry exists before the client
// can see the answer (SPEC-NOTE log timing). Handlers run with s.mu held and must not lock it again.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if d := s.opts.Latency; d > 0 {
		time.Sleep(d)
	}
	rec := &bufResponse{header: http.Header{}, status: http.StatusOK}
	s.serve(rec, r)
	for k, vs := range rec.header {
		w.Header()[k] = vs
	}
	w.WriteHeader(rec.status)
	_, _ = w.Write(rec.body.Bytes())
}

func (s *Server) serve(rec *bufResponse, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		s.log = append(s.log, Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, At: time.Now(), Status: rec.status})
	}()
	s.dispatch(rec, r)
}

func (s *Server) dispatch(w *bufResponse, r *http.Request) {
	if s.unauthorized || !authorised(r) {
		writeError(w, http.StatusUnauthorized, "Client must be authenticated to access this resource.")
		return
	}
	s.counter++
	if th := s.opts.Throttle; th != nil && th.Every > 0 && s.counter%th.Every == 0 {
		w.Header().Set("Retry-After", strconv.FormatInt(retryAfterSeconds(th.RetryAfter), 10))
		writeError(w, http.StatusTooManyRequests, "Rate limit exceeded.")
		return
	}
	if code, ok := s.injectFailure(r.URL.Path); ok {
		writeError(w, code, "Injected failure.")
		return
	}
	s.routes().ServeHTTP(w, r)
}

// injectFailure applies the first FailStatus entry whose prefix matches and still has budget. Each entry keeps its
// own counter (keyed by its position in Options.Fail).
func (s *Server) injectFailure(path string) (int, bool) {
	for i, f := range s.opts.Fail {
		if !strings.HasPrefix(path, f.PathPrefix) {
			continue
		}
		key := strconv.Itoa(i)
		if f.Count > 0 && s.failed[key] >= f.Count {
			continue
		}
		s.failed[key]++
		return f.Code, true
	}
	return 0, false
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /rest/api/3/myself", s.handleMyself)
	mux.HandleFunc("GET /rest/api/3/field", s.handleField)
	mux.HandleFunc("GET /rest/api/3/project/search", s.handleProjectSearch)
	mux.HandleFunc("GET /rest/agile/1.0/board", s.handleBoardSearch)
	mux.HandleFunc("GET /rest/agile/1.0/board/{id}/configuration", s.handleBoardConfiguration)
	mux.HandleFunc("GET /rest/agile/1.0/board/{id}/sprint", s.handleSprint)
	mux.HandleFunc("GET /rest/api/3/status", s.handleStatus)
	mux.HandleFunc("POST /rest/api/3/changelog/bulkfetch", s.handleBulkFetch)
	mux.HandleFunc("GET /rest/api/3/issue/{id}/comment", s.handleComments)
	mux.HandleFunc("GET /rest/dev-status/latest/issue/summary", s.handleDevSummary)
	mux.HandleFunc("GET /rest/dev-status/latest/issue/detail", s.handleDevDetail)
	mux.HandleFunc("GET /rest/api/3/user/assignable/search", s.handleAssignableSearch)
	mux.HandleFunc("POST /rest/api/3/search/jql", s.handleSearch)
	mux.HandleFunc("POST /rest/api/3/search/approximate-count", s.handleApproxCount)
	mux.HandleFunc("POST /rest/api/3/jql/parse", s.handleJQLParse)
	return mux
}

// authorised accepts Basic credentials whose password is Token (any email, including empty).
func authorised(r *http.Request) bool {
	h := r.Header.Get("Authorization")
	const prefix = "Basic "
	if !strings.HasPrefix(h, prefix) {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(h[len(prefix):])
	if err != nil {
		return false
	}
	_, token, ok := strings.Cut(string(raw), ":")
	return ok && token == Token
}

func retryAfterSeconds(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return int64((d + time.Second - 1) / time.Second)
}

// bufResponse collects a response so it can be logged before it is sent.
type bufResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (b *bufResponse) Header() http.Header         { return b.header }
func (b *bufResponse) WriteHeader(code int)        { b.status = code }
func (b *bufResponse) Write(p []byte) (int, error) { return b.body.Write(p) }
