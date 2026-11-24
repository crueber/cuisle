package update

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// MaxRedirects caps the redirect chain of every update request (GitHub asset downloads take one hop to
// objects.githubusercontent.com).
const MaxRedirects = 5

// RedirectPolicy is the CheckRedirect of every client the updater uses: each hop must be https (http only when
// allowHTTP, for tests) and the chain is capped at MaxRedirects. Without it, a redirect could take a download that
// started on https to plain http.
func RedirectPolicy(allowHTTP bool) func(req *http.Request, via []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= MaxRedirects {
			return fmt.Errorf("update: more than %d redirects", MaxRedirects)
		}
		if req.URL.Scheme == "https" || (allowHTTP && req.URL.Scheme == "http") {
			return nil
		}
		return fmt.Errorf("update: refusing a redirect to %s", req.URL.Scheme)
	}
}

// withRedirectPolicy returns a copy of c whose CheckRedirect is RedirectPolicy(allowHTTP) (c itself is not changed).
func withRedirectPolicy(c *http.Client, allowHTTP bool) *http.Client {
	if c == nil {
		c = &http.Client{}
	}
	cp := *c
	cp.CheckRedirect = RedirectPolicy(allowHTTP)
	return &cp
}

// errHost is wrapped when a stored URL points somewhere the channel does not publish from.
var errHost = errors.New("update: download URL is not on the release host")

// hostAllowed reports whether a download URL may be used for this channel: the releases API's own host, and for the
// GitHub channel also github.com and *.githubusercontent.com (where release assets are served).
func hostAllowed(channel, releaseAPI, raw string) bool {
	api, err := url.Parse(releaseAPI)
	if err != nil || api.Host == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Host, api.Host) {
		return true
	}
	if channel == ChannelGitHub && u.Port() == "" {
		h := strings.ToLower(u.Hostname())
		return h == "github.com" || strings.HasSuffix(h, ".githubusercontent.com")
	}
	return false
}

// trusted checks every URL of a found release against the channel's hosts.
func (f *Found) trusted(channel, releaseAPI string) error {
	urls := []string{f.Asset.URL, f.Sums.URL}
	if f.Sig != nil {
		urls = append(urls, f.Sig.URL)
	}
	for _, u := range urls {
		if !hostAllowed(channel, releaseAPI, u) {
			return fmt.Errorf("%w: %s", errHost, u)
		}
	}
	return nil
}
