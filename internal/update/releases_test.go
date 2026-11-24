package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Trimmed real-shape responses. GitHub: GET /repos/o/r/releases. Forgejo: GET /api/v1/repos/o/r/releases (extra
// fields such as id, uuid and author are present and ignored).
const githubJSON = `[
 {"url":"x","html_url":"https://github.com/crueber/cuisle/releases/tag/v1.3.0","id":3,"tag_name":"v1.3.0","name":"Cuisle v1.3.0",
  "draft":false,"prerelease":false,"created_at":"2026-11-20T10:00:00Z","published_at":"2026-11-20T10:05:00Z",
  "assets":[{"id":1,"name":"Cuisle-v1.3.0-macos-universal.dmg","size":11,"browser_download_url":"https://github.com/crueber/cuisle/releases/download/v1.3.0/Cuisle-v1.3.0-macos-universal.dmg"},
            {"id":2,"name":"SHA256SUMS.txt","size":90,"browser_download_url":"https://github.com/crueber/cuisle/releases/download/v1.3.0/SHA256SUMS.txt"}]},
 {"html_url":"https://github.com/crueber/cuisle/releases/tag/v1.4.0-rc.1","tag_name":"v1.4.0-rc.1","draft":false,"prerelease":true,
  "published_at":"2026-11-21T10:05:00Z","assets":[]},
 {"html_url":"","tag_name":"v2.0.0","draft":true,"prerelease":false,"published_at":null,"assets":[]}
]`

const forgejoJSON = `[
 {"id":7,"tag_name":"v1.3.0","target_commitish":"main","name":"Cuisle v1.3.0 (Linux)","body":"Linux AppImage and .deb",
  "url":"https://forge.example/api/v1/repos/crueber/cuisle/releases/7","html_url":"https://forge.example/crueber/cuisle/releases/tag/v1.3.0",
  "tarball_url":"t","zipball_url":"z","upload_url":"u","draft":false,"prerelease":false,"created_at":"2026-11-20T10:00:00Z",
  "published_at":"2026-11-20T10:05:00Z","author":{"id":1,"login":"crueber"},
  "assets":[{"id":11,"name":"cuisle-x86_64.AppImage","size":5,"download_count":0,"created_at":"2026-11-20T10:06:00Z","uuid":"a",
             "browser_download_url":"https://forge.example/crueber/cuisle/releases/download/v1.3.0/cuisle-x86_64.AppImage","type":"attachment"},
            {"id":12,"name":"SHA256SUMS.txt","size":90,"uuid":"b","browser_download_url":"https://forge.example/crueber/cuisle/releases/download/v1.3.0/SHA256SUMS.txt"}]}
]`

func serveJSON(t *testing.T, body string, seen *[]*http.Request) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.Clone(context.Background()))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGitHubList_ParsesAndSendsOnlyAGenericUserAgent(t *testing.T) {
	var seen []*http.Request
	srv := serveJSON(t, githubJSON, &seen)
	rs, err := NewReleases(ChannelGitHub, srv.URL+"/repos/crueber/cuisle/releases", srv.Client(), "Cuisle/v1.2.0").List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 3 || rs[0].Tag != "v1.3.0" || rs[0].PageURL == "" || len(rs[0].Assets) != 2 {
		t.Fatalf("parsed %+v", rs)
	}
	if rs[0].Assets[0].Name != "Cuisle-v1.3.0-macos-universal.dmg" || rs[0].Assets[0].Size != 11 ||
		!strings.HasSuffix(rs[0].Assets[0].URL, ".dmg") {
		t.Fatalf("asset %+v", rs[0].Assets[0])
	}
	if !rs[1].Prerelease || !rs[2].Draft {
		t.Fatalf("flags lost: %+v", rs[1:])
	}
	if len(seen) != 1 {
		t.Fatalf("%d requests", len(seen))
	}
	r := seen[0]
	if r.Method != http.MethodGet || r.URL.Path != "/repos/crueber/cuisle/releases" || r.URL.Query().Get("per_page") != "20" {
		t.Fatalf("request %s %s", r.Method, r.URL)
	}
	if got := r.Header.Get("User-Agent"); got != "Cuisle/v1.2.0" {
		t.Fatalf("User-Agent %q", got)
	}
	if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
		t.Fatalf("request carries identity headers: %v", r.Header)
	}
	for k := range r.Header {
		switch k {
		case "User-Agent", "Accept", "Accept-Encoding":
		default:
			t.Fatalf("unexpected header %s", k)
		}
	}
}

func TestForgejoList_Parses(t *testing.T) {
	var seen []*http.Request
	srv := serveJSON(t, forgejoJSON, &seen)
	rs, err := NewReleases(ChannelForgejo, srv.URL+"/api/v1/repos/crueber/cuisle/releases", srv.Client(), "Cuisle/v1.2.0").List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Tag != "v1.3.0" || len(rs[0].Assets) != 2 || rs[0].Assets[0].Name != "cuisle-x86_64.AppImage" {
		t.Fatalf("parsed %+v", rs)
	}
	if seen[0].URL.Query().Get("limit") != "20" {
		t.Fatalf("query %s", seen[0].URL.RawQuery)
	}
}

func TestList_HTTPErrorAndBadJSON(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" {
			_, _ = w.Write([]byte("<html>"))
			return
		}
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()
	for _, p := range []string{"/x", "/bad"} {
		if _, err := NewGitHub(srv.URL+p, srv.Client(), "Cuisle/v1").List(context.Background()); err == nil {
			t.Fatalf("%s: want an error", p)
		}
	}
}

func TestNewReleases_UnknownChannelIsNil(t *testing.T) {
	if NewReleases("", "https://x", nil, "") != nil || NewReleases("gitlab", "https://x", nil, "") != nil ||
		NewReleases(ChannelGitHub, "", nil, "") != nil {
		t.Fatal("want nil")
	}
}
