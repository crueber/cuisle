package limiter

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Header parsing for jira-sync §3.1. Jira sends X-RateLimit-Limit/-Remaining/-Reset/-NearLimit and the structured
// RateLimit / RateLimit-Policy pair (`"jira-burst-based";r=90;t=1` and `"jira-burst-based";q=100;w=1`), usually only
// near a limit. All are optional; absent or malformed values are ignored.

// nearLimitFraction is jira-sync §3.1: "Remaining/Limit < 0.2" (Atlassian's own NearLimit threshold is 20% left).
const nearLimitFraction = 0.2

// defaultRetryAfter is jira-sync §3 / §3.1: the fallback when a 429 names no wait at all.
const defaultRetryAfter = 5 * time.Second

// Signal reports whether the response says the caller is near a limit: X-RateLimit-NearLimit: true, or
// remaining/limit < 0.2 from either header family.
func Signal(h http.Header) bool {
	if strings.EqualFold(strings.TrimSpace(h.Get("X-RateLimit-NearLimit")), "true") {
		return true
	}
	if lim, ok1 := number(h.Get("X-RateLimit-Limit")); ok1 && lim > 0 {
		if rem, ok2 := number(h.Get("X-RateLimit-Remaining")); ok2 && rem/lim < nearLimitFraction {
			return true
		}
	}
	return structuredNearLimit(h)
}

// RetryAfter is the wait a 429 asks for: Retry-After (seconds or an HTTP date), else X-RateLimit-Reset (RFC 3339 or
// epoch seconds), else the t= of the structured RateLimit header, else 5 s. Never negative.
func RetryAfter(h http.Header, now time.Time) time.Duration {
	if s := strings.TrimSpace(h.Get("Retry-After")); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			return time.Duration(n) * time.Second
		}
		if t, err := http.ParseTime(s); err == nil {
			return nonNegative(t.Sub(now))
		}
	}
	if s := strings.TrimSpace(h.Get("X-RateLimit-Reset")); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return nonNegative(t.Sub(now))
		}
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return nonNegative(time.Unix(n, 0).Sub(now))
		}
	}
	best := time.Duration(-1)
	for _, it := range items(h.Values("RateLimit")) {
		if v, ok := number(it.params["t"]); ok && v >= 0 && !math.IsInf(v, 0) {
			if d := time.Duration(v * float64(time.Second)); d > best {
				best = d
			}
		}
	}
	if best >= 0 {
		return best
	}
	return defaultRetryAfter
}

func nonNegative(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}

func number(s string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(v) {
		return 0, false
	}
	return v, true
}

// item is one entry of a structured header: a (usually quoted) policy name and its key=value parameters.
type item struct {
	name   string
	params map[string]string
}

// items parses `"a";r=1;t=2, "b";r=3` (and the older `limit=100, remaining=10` list form, where every comma part is its
// own anonymous item) into entries.
func items(values []string) []item {
	var out []item
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			it := item{params: map[string]string{}}
			for i, seg := range strings.Split(part, ";") {
				seg = strings.TrimSpace(seg)
				if seg == "" {
					continue
				}
				if k, val, ok := strings.Cut(seg, "="); ok {
					it.params[strings.ToLower(strings.TrimSpace(k))] = strings.Trim(strings.TrimSpace(val), `"`)
				} else if i == 0 {
					it.name = strings.Trim(seg, `"`)
				}
			}
			if len(it.params) > 0 {
				out = append(out, it)
			}
		}
	}
	return out
}

// structuredNearLimit applies r/q < 0.2 to the RateLimit and RateLimit-Policy headers. The quota q comes from the
// RateLimit-Policy entry of the same name, from q= inside the RateLimit entry, or (one policy only) the lone policy.
func structuredNearLimit(h http.Header) bool {
	rl := items(h.Values("RateLimit"))
	if len(rl) == 0 {
		return false
	}
	if anon := mergeAnonymous(rl); anon != nil { // the older `limit=100, remaining=10, reset=3` list form: one entry
		rl = anon
	}
	pol := items(h.Values("RateLimit-Policy"))
	quota := map[string]float64{}
	for _, p := range pol {
		if q, ok := number(p.params["q"]); ok && q > 0 {
			quota[p.name] = q
		}
	}
	for _, it := range rl {
		rem, ok := number(firstOf(it.params, "r", "remaining"))
		if !ok {
			continue
		}
		q, ok := number(firstOf(it.params, "q", "limit"))
		if !ok || q <= 0 {
			q, ok = quota[it.name]
			if !ok && len(quota) == 1 {
				for _, only := range quota {
					q, ok = only, true
				}
			}
		}
		if ok && q > 0 && rem/q < nearLimitFraction {
			return true
		}
	}
	return false
}

// mergeAnonymous folds a list of unnamed single-parameter entries into one; nil when any entry is named (or there is
// only one entry), meaning the list is already in the structured form.
func mergeAnonymous(in []item) []item {
	if len(in) < 2 {
		return nil
	}
	merged := item{params: map[string]string{}}
	for _, it := range in {
		if it.name != "" {
			return nil
		}
		for k, v := range it.params {
			merged.params[k] = v
		}
	}
	return []item{merged}
}

func firstOf(m map[string]string, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return ""
}
