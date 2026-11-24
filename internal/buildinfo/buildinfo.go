// Package buildinfo carries version metadata injected with -ldflags -X.
// FROZEN CONTRACT: variables, types and signatures. Implementation: WP-01.
package buildinfo

import "runtime"

// Set by -ldflags "-X github.com/crueber/cuisle/internal/buildinfo.Version=…".
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// Update channel (ADR 0016), set by -ldflags -X in the release workflows only. Local and dev builds leave Channel
// empty and never check for updates.
//
//	Channel       "github" or "forgejo": where this copy was built and published
//	ReleaseAPI    the channel's releases-list endpoint (…/repos/<owner>/<repo>/releases)
//	ReleasePubKey base64 ed25519 public key (internal/relsign); when set, a release's SHA256SUMS.txt.sig is required
var (
	Channel       = ""
	ReleaseAPI    = ""
	ReleasePubKey = ""
)

type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"goVersion"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	Dev       bool   `json:"dev"` // Version == "dev"
}

// Get returns the build info (GoVersion/OS/Arch from runtime).
func Get() Info {
	return Info{
		Version:   Version,
		Commit:    Commit,
		Date:      Date,
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		Dev:       Version == "dev",
	}
}

// UserAgent returns "Cuisle/<version> (<os>)".
func UserAgent() string { return "Cuisle/" + Version + " (" + runtime.GOOS + ")" }
