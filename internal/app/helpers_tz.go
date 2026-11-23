package app

import (
	"os"
	"strings"
	"time"
)

// ianaLocation returns a named IANA zone for exports (export.Snapshot.Timezone is "an IANA name"). time.Local prints
// as "Local", so it is resolved from TZ or the /etc/localtime symlink, falling back to UTC. Named zones pass through.
func ianaLocation(loc *time.Location) *time.Location {
	if loc != nil && loc.String() != "Local" && loc.String() != "" {
		return loc
	}
	return localIANA(os.Getenv("TZ"), func() (string, error) { return os.Readlink("/etc/localtime") })
}

func localIANA(tz string, readLink func() (string, error)) *time.Location {
	if name := strings.TrimPrefix(tz, ":"); name != "" && name != "Local" {
		if loc, err := time.LoadLocation(name); err == nil {
			return loc
		}
	}
	if target, err := readLink(); err == nil {
		if _, name, ok := strings.Cut(target, "zoneinfo/"); ok && name != "" {
			if loc, err := time.LoadLocation(name); err == nil {
				return loc
			}
		}
	}
	return time.UTC
}
