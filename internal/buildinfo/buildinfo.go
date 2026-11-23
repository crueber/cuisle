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
