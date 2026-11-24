// Package update checks the channel this copy was downloaded from for a newer release and installs it (ADR 0016).
//
// A copy built by the GitHub release workflow checks GitHub releases; a copy built by Forgejo CI checks Forgejo
// releases. Local and dev builds have no channel and never check. The check is one anonymous HTTPS GET of the
// public releases list with the User-Agent "Cuisle/<version>": no identifiers, cookies or machine details.
//
// Installing downloads this platform's asset, verifies it against the release's SHA256SUMS.txt (and its ed25519
// signature when the build carries a public key), and only then hands it to the OS-specific Installer.
//
// The package imports nothing from this module except relsign and apperr, and never starts a timer: callers decide
// when to check (app start when the last check is a day old, the day rollover, or Check now).
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Channel names (buildinfo.Channel).
const (
	ChannelGitHub  = "github"
	ChannelForgejo = "forgejo"
)

// SumsName and SigName are the checksum file and its optional ed25519 signature on every release.
const (
	SumsName = "SHA256SUMS.txt"
	SigName  = "SHA256SUMS.txt.sig"
)

// Asset is one file attached to a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Size int64  `json:"size"`
}

// Release is one published release, provider-neutral.
type Release struct {
	Tag         string    `json:"tag"`
	Version     Version   `json:"-"`
	Name        string    `json:"name"`
	PageURL     string    `json:"pageUrl"`
	PublishedAt time.Time `json:"publishedAt"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	Assets      []Asset   `json:"assets"`
}

// Asset returns the named asset.
func (r Release) Asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// Releases lists a channel's published releases (newest first is not assumed).
type Releases interface {
	List(ctx context.Context) ([]Release, error)
}

// listLimit is how many releases one request asks for; the newest stable one is always among them.
const listLimit = 20

// maxListBytes caps the releases-list response.
const maxListBytes = 4 << 20

// httpReleases is the GitHub or Forgejo REST client. Both return a JSON array of releases with the same field names
// for everything Cuisle reads (tag_name, html_url, draft, prerelease, published_at, assets[].name,
// assets[].browser_download_url, assets[].size); the query parameter for the page size differs.
type httpReleases struct {
	kind      string
	listURL   string
	client    *http.Client
	userAgent string
}

// NewGitHub lists releases from a GitHub REST endpoint such as https://api.github.com/repos/<owner>/<repo>/releases.
func NewGitHub(listURL string, client *http.Client, userAgent string) Releases {
	return &httpReleases{kind: ChannelGitHub, listURL: listURL, client: withRedirectPolicy(client, false), userAgent: userAgent}
}

// NewForgejo lists releases from a Forgejo/Gitea endpoint such as https://<host>/api/v1/repos/<owner>/<repo>/releases.
func NewForgejo(listURL string, client *http.Client, userAgent string) Releases {
	return &httpReleases{kind: ChannelForgejo, listURL: listURL, client: withRedirectPolicy(client, false), userAgent: userAgent}
}

// NewReleases returns the client for a channel, or nil for an unknown or empty channel.
func NewReleases(channel, listURL string, client *http.Client, userAgent string) Releases {
	if listURL == "" {
		return nil
	}
	switch channel {
	case ChannelGitHub:
		return NewGitHub(listURL, client, userAgent)
	case ChannelForgejo:
		return NewForgejo(listURL, client, userAgent)
	}
	return nil
}

type wireAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type wireRelease struct {
	TagName     string      `json:"tag_name"`
	Name        string      `json:"name"`
	HTMLURL     string      `json:"html_url"`
	Draft       bool        `json:"draft"`
	Prerelease  bool        `json:"prerelease"`
	PublishedAt time.Time   `json:"published_at"`
	Assets      []wireAsset `json:"assets"`
}

func (h *httpReleases) List(ctx context.Context) ([]Release, error) {
	u, err := url.Parse(h.listURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("update: bad releases URL")
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("update: refusing a %s releases URL", u.Scheme)
	}
	q := u.Query()
	if h.kind == ChannelGitHub {
		q.Set("per_page", fmt.Sprint(listLimit))
	} else {
		q.Set("limit", fmt.Sprint(listLimit))
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", h.userAgent)
	req.Header.Set("Accept", "application/json")
	if h.kind == ChannelGitHub {
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return nil, fmt.Errorf("update: releases list answered HTTP %d", resp.StatusCode)
	}
	var wire []wireRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxListBytes)).Decode(&wire); err != nil {
		return nil, fmt.Errorf("update: releases list is not JSON: %w", err)
	}
	out := make([]Release, 0, len(wire))
	for _, w := range wire {
		r := Release{
			Tag: w.TagName, Name: w.Name, PageURL: w.HTMLURL, PublishedAt: w.PublishedAt,
			Draft: w.Draft, Prerelease: w.Prerelease, Assets: make([]Asset, 0, len(w.Assets)),
		}
		for _, a := range w.Assets {
			r.Assets = append(r.Assets, Asset{Name: a.Name, URL: a.URL, Size: a.Size})
		}
		out = append(out, r)
	}
	return out, nil
}
