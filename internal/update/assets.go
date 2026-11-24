package update

import (
	"regexp"
	"strconv"
	"strings"
)

// Kind is how an update is applied on this machine.
type Kind string

const (
	KindDMG      Kind = "dmg"      // macOS: mount, copy the .app next to the running bundle, swap, relaunch
	KindNSIS     Kind = "nsis"     // Windows: run the NSIS installer silently, then relaunch
	KindAppImage Kind = "appimage" // Linux AppImage: replace the file in place, relaunch
	KindPage     Kind = "page"     // anything else (Linux .deb needs root): open the release page instead
)

// assetRule is one row of the selection table: on GOOS, an asset whose name ends with Suffix applies with Kind.
// Names are matched by pattern, not exactly, because the Linux names are not versioned and the others are
// (release.yml: Cuisle-vX.Y.Z-macos-universal.dmg, Cuisle-vX.Y.Z-windows-amd64-setup.exe; Forgejo ci.yml and
// release.yml: bin/*.AppImage, bin/*.deb).
type assetRule struct {
	GOOS      string
	Kind      Kind
	Suffix    string   // case-insensitive
	Universal []string // name tokens that mean "every architecture"
	BareArch  string   // the architecture assumed when the name carries no architecture token
	AppImage  bool     // the rule applies only when running from an AppImage ($APPIMAGE set)
	NotImage  bool     // the rule applies only when NOT running from an AppImage
}

// assetRules is the selection table, in preference order per platform.
var assetRules = []assetRule{
	{GOOS: "darwin", Kind: KindDMG, Suffix: ".dmg", Universal: []string{"universal"}},
	{GOOS: "windows", Kind: KindNSIS, Suffix: "-setup.exe", BareArch: "amd64"},
	{GOOS: "windows", Kind: KindNSIS, Suffix: "-installer.exe", BareArch: "amd64"},
	{GOOS: "linux", Kind: KindAppImage, Suffix: ".appimage", BareArch: "amd64", AppImage: true},
	{GOOS: "linux", Kind: KindPage, Suffix: ".deb", BareArch: "amd64", NotImage: true},
	{GOOS: "linux", Kind: KindPage, Suffix: ".appimage", BareArch: "amd64", NotImage: true},
}

// archTokens are the spellings of each GOARCH found in asset names.
var archTokens = map[string][]string{
	"amd64": {"amd64", "x86_64", "x64", "intel"},
	"arm64": {"arm64", "aarch64"},
}

// SelectAsset picks this platform's asset from a release. appImage reports whether the app runs from an AppImage.
// It returns false when the release has nothing for this OS and architecture.
func SelectAsset(assets []Asset, goos, goarch string, appImage bool) (Asset, Kind, bool) {
	for _, rule := range assetRules {
		if rule.GOOS != goos || (rule.AppImage && !appImage) || (rule.NotImage && appImage) {
			continue
		}
		best, bestScore := Asset{}, 0
		for _, a := range assets {
			if s := rule.score(a.Name, goarch); s > bestScore {
				best, bestScore = a, s
			}
		}
		if bestScore > 0 {
			return best, rule.Kind, true
		}
	}
	return Asset{}, "", false
}

// score is 0 when the asset does not fit; higher is a better fit (exact architecture > universal > bare name).
func (r assetRule) score(name, goarch string) int {
	lower := strings.ToLower(name)
	if !strings.HasSuffix(lower, strings.ToLower(r.Suffix)) || strings.HasSuffix(lower, ".sig") {
		return 0
	}
	toks := tokens(strings.TrimSuffix(lower, strings.ToLower(r.Suffix)))
	mine, other, universal := false, false, false
	for _, t := range toks {
		for arch, spellings := range archTokens {
			for _, sp := range spellings {
				if t == sp {
					if arch == goarch {
						mine = true
					} else {
						other = true
					}
				}
			}
		}
		for _, u := range r.Universal {
			if t == u {
				universal = true
			}
		}
	}
	switch {
	case mine:
		return 3
	case universal:
		return 2
	case other:
		return 0
	case r.BareArch == "" || r.BareArch == goarch:
		return 1
	}
	return 0
}

var nameVersionRE = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// nameMatchesVersion: where a dmg or installer name carries a version (Cuisle-v1.3.0-macos-universal.dmg), it must
// be the release's own version, so a release cannot hand out another release's file. Names without a version,
// and the other kinds (the Linux names are not versioned; the .deb carries nfpm's package version), pass.
func nameMatchesVersion(name string, kind Kind, v Version) bool {
	if kind != KindDMG && kind != KindNSIS {
		return true
	}
	for _, m := range nameVersionRE.FindAllStringSubmatch(name, -1) {
		if m[1] != strconv.Itoa(v.Major) || m[2] != strconv.Itoa(v.Minor) || m[3] != strconv.Itoa(v.Patch) {
			return false
		}
	}
	return true
}

func tokens(s string) []string {
	return strings.FieldsFunc(s, func(c rune) bool { return c == '-' || c == '_' || c == '.' || c == ' ' })
}
