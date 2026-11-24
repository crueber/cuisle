package main

import (
	"io"
	"log/slog"
	"net/http"
	"testing"
)

// Local builds, the fake Jira and the startup bench never get an updater, so they can never reach the network
// for an update check (ADR 0016).
func TestNewUpdater_OnlyChannelBuildsCheck(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	base := updaterConfig{channel: "github", releaseAPI: "https://api.github.com/repos/crueber/cuisle/releases", version: "v1.0.0", dataDir: t.TempDir()}
	cases := map[string]func(c *updaterConfig){
		"no channel (task build / task package)": func(c *updaterConfig) { c.channel = "" },
		"fake jira (task dev, tests)":            func(c *updaterConfig) { c.fake = true },
		"bench startup":                          func(c *updaterConfig) { c.bench = true },
		"unknown channel":                        func(c *updaterConfig) { c.channel = "gitlab" },
		"no releases URL":                        func(c *updaterConfig) { c.releaseAPI = "" },
	}
	for name, mut := range cases {
		c := base
		mut(&c)
		if u := newUpdater(c, nil, nil, logger, nil); u != nil {
			t.Errorf("%s: got an updater", name)
		}
	}
	if u := newUpdater(base, nil, nil, logger, nil); u == nil || !u.Supported() {
		t.Fatal("a github build should check")
	}
	c := newDownloadClient()
	tr := c.Transport.(*http.Transport)
	if c.CheckRedirect == nil || !tr.DisableCompression || c.Jar != nil || c.Timeout != 0 {
		t.Fatalf("download client: redirect policy %v, compression off %v, jar %v, timeout %v", c.CheckRedirect != nil, tr.DisableCompression, c.Jar, c.Timeout)
	}
	plainHop, _ := http.NewRequest(http.MethodGet, "http://example.test/x", nil)
	if c.CheckRedirect(plainHop, nil) == nil {
		t.Fatal("the download client follows redirects to http")
	}
	forgejo := base
	forgejo.channel = "forgejo"
	if u := newUpdater(forgejo, nil, nil, logger, nil); u == nil || !u.Supported() {
		t.Fatal("a forgejo build should check")
	}
}
