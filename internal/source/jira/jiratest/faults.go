package jiratest

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseFaults parses CUISLE_FAKE_FAULTS, e.g. "429:every=7,retry=1;latency=80ms;devstatus=404;fail=/rest/agile:503:2;pagesize=5",
// into the fault fields of base (Now/Loc untouched).
//
// Segments are separated by ";". `429:every=N[,retry=S]` sets the throttle (retry in whole seconds, default 1);
// `latency=D`, `devstatus=ok|404|403|garbage` and `pagesize=N` set their fields; `fail=PATH:CODE[:COUNT]` may repeat
// and appends in order (COUNT omitted = 0 = always). An empty spec returns base unchanged. Any malformed segment or
// unknown key is an error.
func ParseFaults(spec string, base Options) (Options, error) {
	if strings.TrimSpace(spec) == "" {
		return base, nil
	}
	out := base
	out.Fail = append([]FailStatus(nil), base.Fail...)
	for _, seg := range strings.Split(spec, ";") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		if err := applyFault(&out, seg); err != nil {
			return base, fmt.Errorf("jiratest: fault %q: %w", seg, err)
		}
	}
	return out, nil
}

func applyFault(o *Options, seg string) error {
	if rest, ok := strings.CutPrefix(seg, "429:"); ok {
		return applyThrottle(o, rest)
	}
	key, val, ok := strings.Cut(seg, "=")
	if !ok {
		return fmt.Errorf("want key=value")
	}
	switch key {
	case "latency":
		d, err := time.ParseDuration(val)
		if err != nil || d < 0 {
			return fmt.Errorf("latency must be a non-negative duration")
		}
		o.Latency = d
	case "devstatus":
		switch val {
		case "ok", "404", "403", "garbage":
			o.DevStatus = val
		default:
			return fmt.Errorf("devstatus must be ok, 404, 403 or garbage")
		}
	case "fail":
		fs, err := parseFail(val)
		if err != nil {
			return err
		}
		o.Fail = append(o.Fail, fs)
	case "pagesize":
		n, err := strconv.Atoi(val)
		if err != nil || n < 0 {
			return fmt.Errorf("pagesize must be a non-negative integer")
		}
		o.PageSize = n
	default:
		return fmt.Errorf("unknown fault %q", key)
	}
	return nil
}

// applyThrottle parses the part after "429:", i.e. "every=N[,retry=S]".
func applyThrottle(o *Options, spec string) error {
	th := &Throttle{RetryAfter: time.Second}
	haveEvery := false
	for _, kv := range strings.Split(spec, ",") {
		key, val, ok := strings.Cut(strings.TrimSpace(kv), "=")
		if !ok {
			return fmt.Errorf("throttle wants every=N[,retry=S]")
		}
		switch key {
		case "every":
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 {
				return fmt.Errorf("every must be a positive integer")
			}
			th.Every = n
			haveEvery = true
		case "retry":
			n, err := strconv.Atoi(val)
			if err != nil || n < 0 {
				return fmt.Errorf("retry must be a non-negative number of seconds")
			}
			th.RetryAfter = time.Duration(n) * time.Second
		default:
			return fmt.Errorf("unknown throttle key %q", key)
		}
	}
	if !haveEvery {
		return fmt.Errorf("throttle needs every=N")
	}
	o.Throttle = th
	return nil
}

// parseFail parses "PATH:CODE[:COUNT]".
func parseFail(val string) (FailStatus, error) {
	parts := strings.Split(val, ":")
	if len(parts) < 2 || len(parts) > 3 || parts[0] == "" {
		return FailStatus{}, fmt.Errorf("fail wants PATH:CODE[:COUNT]")
	}
	code, err := strconv.Atoi(parts[1])
	if err != nil || code < 100 || code > 599 {
		return FailStatus{}, fmt.Errorf("fail code must be an HTTP status")
	}
	fs := FailStatus{PathPrefix: parts[0], Code: code}
	if len(parts) == 3 {
		n, err := strconv.Atoi(parts[2])
		if err != nil || n < 0 {
			return FailStatus{}, fmt.Errorf("fail count must be a non-negative integer")
		}
		fs.Count = n
	}
	return fs, nil
}
