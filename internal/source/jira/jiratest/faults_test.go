package jiratest

// Tests for WP-07 fault injection: Options.{Latency,Throttle,Fail,DevStatus,PageSize} and ParseFaults
// (CUISLE_FAKE_FAULTS, jira-sync.md §6). Each fault is tested in isolation.
//
// SPEC-NOTE (throttle counting): the Throttle counter counts every authorised request the server receives (any path),
// so with Every=3 requests number 3, 6, 9 … answer 429. Retry-After is whole seconds, rounded up (1500ms -> "2").
// A 429 request still appears in the request log with status 429.
//
// SPEC-NOTE (Fail): a FailStatus matches a request when its path has the prefix; the first Count matches answer Code
// (Count 0 = every match). Counters are per FailStatus entry; non-matching paths are untouched.
//
// SPEC-NOTE (PageSize caps): PageSize > 0 caps the page size of the list endpoints that page: board search
// (startAt/isLast), issue comments (startAt/total), and changelog bulkfetch (nextPageToken); search/jql is covered in
// jql_test.go.
//
// SPEC-NOTE (ParseFaults): `retry=` is seconds; `fail=PATH:CODE[:COUNT]` (COUNT omitted = 0 = always) may be repeated
// and entries append in order; segments are separated by ";", the first segment of the throttle is `429:every=N[,retry=S]`.
// An empty spec returns base unchanged. Any malformed segment or unknown key is an error. Now/Loc are never touched.

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func TestThrottle_EveryNthRequestAnswers429WithRetryAfter(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{Throttle: &Throttle{Every: 3, RetryAfter: time.Second}})
	var codes []int
	var retryAfter string
	for i := 0; i < 6; i++ {
		code, hdr, _ := rawCall(t, s, http.MethodGet, "/rest/api/3/myself", nil, basicAuth(Token))
		codes = append(codes, code)
		if code == http.StatusTooManyRequests {
			retryAfter = hdr.Get("Retry-After")
		}
	}
	want := []int{200, 200, 429, 200, 200, 429}
	if !reflect.DeepEqual(codes, want) {
		t.Fatalf("statuses = %v, want %v", codes, want)
	}
	if retryAfter != "1" {
		t.Errorf("Retry-After = %q, want \"1\"", retryAfter)
	}
	log := waitLog(t, s, 6)
	if log[2].Status != 429 || log[5].Status != 429 || log[0].Status != 200 {
		t.Errorf("log statuses = %d,%d,%d… want 200,_,429 …: %+v", log[0].Status, log[1].Status, log[2].Status, log)
	}
}

func TestThrottle_RetryAfterRoundsUpToWholeSeconds(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{Throttle: &Throttle{Every: 1, RetryAfter: 1500 * time.Millisecond}})
	code, hdr, _ := rawCall(t, s, http.MethodGet, "/rest/api/3/myself", nil, basicAuth(Token))
	if code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 (Every=1)", code)
	}
	if got := hdr.Get("Retry-After"); got != "2" {
		t.Fatalf("Retry-After = %q, want \"2\" (1500ms rounded up)", got)
	}
}

func TestThrottle_CountsAcrossEndpoints(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{Throttle: &Throttle{Every: 2, RetryAfter: time.Second}})
	paths := []string{"/rest/api/3/myself", "/rest/api/3/status", "/rest/api/3/field", "/rest/api/3/status"}
	var codes []int
	for _, p := range paths {
		code, _ := get(t, s, p)
		codes = append(codes, code)
	}
	if want := []int{200, 429, 200, 429}; !reflect.DeepEqual(codes, want) {
		t.Fatalf("statuses = %v, want %v", codes, want)
	}
}

func TestFail_FirstCountMatchesFailThenRecover(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{Fail: []FailStatus{{PathPrefix: "/rest/agile", Code: 503, Count: 2}}})
	var codes []int
	for i := 0; i < 4; i++ {
		code, _ := get(t, s, "/rest/agile/1.0/board/1/configuration")
		codes = append(codes, code)
	}
	if want := []int{503, 503, 200, 200}; !reflect.DeepEqual(codes, want) {
		t.Fatalf("statuses = %v, want %v", codes, want)
	}
}

func TestFail_CountZeroFailsEveryMatch(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{Fail: []FailStatus{{PathPrefix: "/rest/api/3/status", Code: 500}}})
	for i := 0; i < 3; i++ {
		if code, _ := get(t, s, "/rest/api/3/status"); code != 500 {
			t.Fatalf("request %d: status %d, want 500 (Count 0 = always)", i, code)
		}
	}
}

func TestFail_NonMatchingPathsAreUntouchedAndLogged(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{Fail: []FailStatus{{PathPrefix: "/rest/agile", Code: 502}}})
	if code, _ := get(t, s, "/rest/api/3/myself"); code != 200 {
		t.Fatalf("myself = %d, want 200", code)
	}
	if code, _ := get(t, s, "/rest/agile/1.0/board?projectKeyOrId=MOB"); code != 502 {
		t.Fatalf("board = %d, want 502", code)
	}
	log := waitLog(t, s, 2)
	if log[1].Path != "/rest/agile/1.0/board" || log[1].Status != 502 {
		t.Errorf("log[1] = %+v, want the failed board request with status 502", log[1])
	}
}

func TestFail_EntriesCountIndependently(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{Fail: []FailStatus{
		{PathPrefix: "/rest/api/3/status", Code: 500, Count: 1},
		{PathPrefix: "/rest/api/3/field", Code: 503, Count: 1},
	}})
	got := []int{}
	for _, p := range []string{"/rest/api/3/status", "/rest/api/3/field", "/rest/api/3/status", "/rest/api/3/field"} {
		code, _ := get(t, s, p)
		got = append(got, code)
	}
	if want := []int{500, 503, 200, 200}; !reflect.DeepEqual(got, want) {
		t.Fatalf("statuses = %v, want %v", got, want)
	}
}

func TestLatency_EveryResponseTakesAtLeastTheConfiguredTime(t *testing.T) {
	defer guard(t)
	const lat = 60 * time.Millisecond
	s := newTiny(t, Options{Latency: lat})
	start := time.Now()
	code, _ := get(t, s, "/rest/api/3/myself")
	elapsed := time.Since(start)
	mustOK(t, code, nil, "myself")
	if elapsed < lat {
		t.Fatalf("request took %v, want at least %v", elapsed, lat)
	}
}

func TestDevStatusModes(t *testing.T) {
	defer guard(t)
	cases := []struct {
		mode       string
		wantStatus int
		wantJSON   bool
	}{
		{"ok", 200, true},
		{"", 200, true}, // default = ok
		{"404", 404, false},
		{"403", 403, false},
		{"garbage", 200, false}, // 200 with a body that is not JSON
	}
	for _, c := range cases {
		sub(t, "mode="+c.mode, func(t *testing.T) {
			s := newTiny(t, Options{DevStatus: c.mode})
			id := issueID(t, s, "MOB-1")
			paths := []string{
				"/rest/dev-status/latest/issue/summary?issueId=" + id,
				"/rest/dev-status/latest/issue/detail?issueId=" + id + "&applicationType=GitHub&dataType=repository",
			}
			for _, p := range paths {
				code, _, body := rawCall(t, s, http.MethodGet, p, nil, basicAuth(Token))
				if code != c.wantStatus {
					t.Errorf("%s: status = %d, want %d", p, code, c.wantStatus)
				}
				if c.wantStatus == 200 && c.wantJSON != jsonValid(body) {
					t.Errorf("%s: body valid JSON = %v, want %v (body %q)", p, jsonValid(body), c.wantJSON, body)
				}
			}
			// Other endpoints are unaffected.
			if code, _ := get(t, s, "/rest/api/3/myself"); code != 200 {
				t.Errorf("myself = %d, want 200 (DevStatus only affects dev-status)", code)
			}
			log := waitLog(t, s, 3)
			if log[0].Status != c.wantStatus {
				t.Errorf("log status of the summary call = %d, want %d", log[0].Status, c.wantStatus)
			}
		})
	}
}

func jsonValid(b []byte) bool { return json.Valid(b) }

func TestPageSize_CapsBoardSearchAndSetsIsLast(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{PageSize: 1})
	var names []string
	startAt := 0
	for page := 0; page < 10; page++ {
		code, v := get(t, s, "/rest/agile/1.0/board?projectKeyOrId=MOB&maxResults=50&startAt="+strconv.Itoa(startAt))
		mustOK(t, code, v, "board page")
		vals := jarr(jget(v, "values"))
		if len(vals) != 1 && !(len(vals) == 0 && jget(v, "isLast") == true) {
			t.Fatalf("page %d has %d values, want 1 (PageSize=1 beats maxResults=50)", page, len(vals))
		}
		for _, b := range vals {
			names = append(names, jstr(jget(b, "name")))
		}
		if jget(v, "isLast") == true {
			if want := []string{"MOB Kanban", "MOB board"}; !reflect.DeepEqual(sortedStrings(names), want) {
				t.Fatalf("boards over %d pages = %v, want %v", page+1, names, want)
			}
			if page < 1 {
				t.Fatalf("2 boards arrived in a single page despite PageSize=1")
			}
			return
		}
		startAt += len(vals)
	}
	t.Fatalf("board paging did not end with isLast=true within 10 pages; got %v", names)
}

func TestPageSize_CapsCommentPages(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{PageSize: 2})
	id := issueID(t, s, "MOB-1")
	code, v := get(t, s, "/rest/api/3/issue/"+id+"/comment?orderBy=-created&maxResults=100&startAt=0")
	mustOK(t, code, v, "comments")
	if n := len(jarr(jget(v, "comments"))); n != 2 {
		t.Fatalf("first page has %d comments, want 2 (PageSize caps maxResults=100)", n)
	}
	if jnum(jget(v, "total")) != 3 {
		t.Errorf("total = %v, want 3 (the total is not capped)", jget(v, "total"))
	}
	code, v = get(t, s, "/rest/api/3/issue/"+id+"/comment?orderBy=-created&maxResults=100&startAt=2")
	mustOK(t, code, v, "comments page 2")
	if n := len(jarr(jget(v, "comments"))); n != 1 {
		t.Fatalf("second page has %d comments, want 1", n)
	}
}

// ── ParseFaults ─────────────────────────────────────────────────────────────────────────────────

func TestParseFaults_DocumentedExample(t *testing.T) {
	defer guard(t)
	base := Options{Now: testNow, Loc: nyc}
	got, err := ParseFaults("429:every=7,retry=1;latency=80ms;devstatus=404;fail=/rest/agile:503:2;pagesize=5", base)
	if err != nil {
		t.Fatalf("ParseFaults: %v", err)
	}
	if got.Throttle == nil || got.Throttle.Every != 7 || got.Throttle.RetryAfter != time.Second {
		t.Errorf("Throttle = %+v, want {Every:7 RetryAfter:1s}", got.Throttle)
	}
	if got.Latency != 80*time.Millisecond {
		t.Errorf("Latency = %v, want 80ms", got.Latency)
	}
	if got.DevStatus != "404" {
		t.Errorf("DevStatus = %q, want 404", got.DevStatus)
	}
	if want := []FailStatus{{PathPrefix: "/rest/agile", Code: 503, Count: 2}}; !reflect.DeepEqual(got.Fail, want) {
		t.Errorf("Fail = %+v, want %+v", got.Fail, want)
	}
	if got.PageSize != 5 {
		t.Errorf("PageSize = %d, want 5", got.PageSize)
	}
	if !got.Now.Equal(testNow) || got.Loc != nyc {
		t.Errorf("Now/Loc were touched: %v %v", got.Now, got.Loc)
	}
}

func TestParseFaults_EmptySpecReturnsBaseUnchanged(t *testing.T) {
	defer guard(t)
	base := Options{Now: testNow, Loc: nyc, PageSize: 9, DevStatus: "403", Latency: time.Second}
	got, err := ParseFaults("", base)
	if err != nil {
		t.Fatalf("ParseFaults: %v", err)
	}
	if !reflect.DeepEqual(got, base) {
		t.Fatalf("got %+v, want base %+v", got, base)
	}
}

func TestParseFaults_SingleSegments(t *testing.T) {
	defer guard(t)
	base := Options{Now: testNow, Loc: nyc}
	t.Run("throttle only", func(t *testing.T) {
		defer guard(t)
		got, err := ParseFaults("429:every=4", base)
		if err != nil || got.Throttle == nil || got.Throttle.Every != 4 {
			t.Fatalf("got %+v err %v, want Throttle.Every 4", got.Throttle, err)
		}
	})
	t.Run("retry fractional-free seconds", func(t *testing.T) {
		defer guard(t)
		got, err := ParseFaults("429:every=2,retry=3", base)
		if err != nil || got.Throttle == nil || got.Throttle.RetryAfter != 3*time.Second {
			t.Fatalf("got %+v err %v, want RetryAfter 3s", got.Throttle, err)
		}
	})
	t.Run("latency only", func(t *testing.T) {
		defer guard(t)
		got, err := ParseFaults("latency=250ms", base)
		if err != nil || got.Latency != 250*time.Millisecond || got.Throttle != nil {
			t.Fatalf("got %+v err %v", got, err)
		}
	})
	t.Run("fail without count means always", func(t *testing.T) {
		defer guard(t)
		got, err := ParseFaults("fail=/rest/api/3/search:500", base)
		want := []FailStatus{{PathPrefix: "/rest/api/3/search", Code: 500, Count: 0}}
		if err != nil || !reflect.DeepEqual(got.Fail, want) {
			t.Fatalf("got %+v err %v, want %+v", got.Fail, err, want)
		}
	})
	t.Run("repeated fail entries append in order", func(t *testing.T) {
		defer guard(t)
		got, err := ParseFaults("fail=/a:500:1;fail=/b:503:2", base)
		want := []FailStatus{{PathPrefix: "/a", Code: 500, Count: 1}, {PathPrefix: "/b", Code: 503, Count: 2}}
		if err != nil || !reflect.DeepEqual(got.Fail, want) {
			t.Fatalf("got %+v err %v, want %+v", got.Fail, err, want)
		}
	})
	for _, mode := range []string{"ok", "404", "403", "garbage"} {
		t.Run("devstatus="+mode, func(t *testing.T) {
			defer guard(t)
			got, err := ParseFaults("devstatus="+mode, base)
			if err != nil || got.DevStatus != mode {
				t.Fatalf("got %q err %v, want %q", got.DevStatus, err, mode)
			}
		})
	}
}

func TestParseFaults_MalformedSpecsAreErrors(t *testing.T) {
	defer guard(t)
	for _, spec := range []string{
		"bogus=1",
		"latency=soon",
		"429:every=x",
		"429:every=3,retry=abc",
		"fail=/rest/agile",
		"fail=/rest/agile:abc:1",
		"fail=/rest/agile:503:many",
		"devstatus=maybe",
		"pagesize=abc",
	} {
		sub(t, spec, func(t *testing.T) {
			if _, err := ParseFaults(spec, Options{}); err == nil {
				t.Fatalf("ParseFaults(%q) succeeded, want an error", spec)
			}
		})
	}
}
