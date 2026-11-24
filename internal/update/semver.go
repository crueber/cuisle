package update

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a release version vMAJOR.MINOR.PATCH. Pre is the pre-release suffix ("rc.1"); such versions are never
// offered as updates.
type Version struct {
	Major, Minor, Patch int
	Pre                 string
}

// ParseVersion reads "v1.2.3", "1.2.3", "v1.2.3-rc.1" or "v1.2.3+build" (build metadata is ignored).
func ParseVersion(s string) (Version, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	var v Version
	if i := strings.IndexByte(s, '-'); i >= 0 {
		v.Pre = s[i+1:]
		s = s[:i]
		if v.Pre == "" {
			return Version{}, false
		}
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, false
	}
	nums := [3]int{}
	for i, p := range parts {
		if p == "" || len(p) > 9 {
			return Version{}, false
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || strconv.Itoa(n) != p {
			return Version{}, false
		}
		nums[i] = n
	}
	v.Major, v.Minor, v.Patch = nums[0], nums[1], nums[2]
	return v, true
}

// String is "vX.Y.Z" (with "-pre" when set).
func (v Version) String() string {
	s := fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// Compare orders by major, minor, patch; a pre-release sorts before its release (semver §11), and two
// pre-releases of the same version compare by their suffix text.
func (v Version) Compare(o Version) int {
	for _, d := range [3]int{v.Major - o.Major, v.Minor - o.Minor, v.Patch - o.Patch} {
		if d < 0 {
			return -1
		}
		if d > 0 {
			return 1
		}
	}
	switch {
	case v.Pre == o.Pre:
		return 0
	case v.Pre == "":
		return 1
	case o.Pre == "":
		return -1
	}
	return strings.Compare(v.Pre, o.Pre)
}

// Latest returns the newest stable release: drafts, pre-releases and tags that are not versions are skipped.
func Latest(rs []Release) (Release, bool) {
	var best Release
	found := false
	for _, r := range rs {
		if r.Draft || r.Prerelease {
			continue
		}
		v, ok := ParseVersion(r.Tag)
		if !ok || v.Pre != "" {
			continue
		}
		r.Version = v
		if !found || v.Compare(best.Version) > 0 {
			best, found = r, true
		}
	}
	return best, found
}
