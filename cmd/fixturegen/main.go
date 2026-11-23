// Command fixturegen converts design/reference/sample-data.json into
// internal/source/jira/jiratest/testdata/fixture.json (jira-sync.md §6 conversion rules). Implementation: WP-08.
//
//	go run ./cmd/fixturegen [-in design/reference/sample-data.json] [-out internal/source/jira/jiratest/testdata/fixture.json]
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/crueber/cuisle/internal/source/jira/jiratest"
)

// Convert maps the sample shape (standups[].{key,name,board,people[],tickets[{k,a,s,ty,t,days,bl,blD,ev[28],cm[28],
// hist[{s,d}]}], backlog[]}) to a Fixture: one project + one board (scrum unless the sample says Kanban) per standup,
// columns To Do / In Progress / Code Review / QA / Done, ev 1|2 → that many commits on day i−27, ev 3 → one PR,
// cm true → one comment, bl/blD → flaggedDays [[-(blD−1), 0]], backlog in array order. Deterministic.
func Convert(sample []byte) (*jiratest.Fixture, error) {
	var doc sampleDoc
	if err := json.Unmarshal(sample, &doc); err != nil {
		return nil, fmt.Errorf("parse sample: %w", err)
	}
	if len(doc.Standups) == 0 {
		return nil, errors.New("sample has no standups")
	}
	fx := &jiratest.Fixture{
		Projects: []jiratest.FxProject{}, Boards: []jiratest.FxBoard{}, Users: []jiratest.FxUser{},
		Issues: []jiratest.FxIssue{}, Backlog: []jiratest.FxBacklog{},
	}
	seenUser := map[string]bool{}
	nextPR := 1
	for i, st := range doc.Standups {
		boardID := i + 1
		boardName, kind, err := parseBoardLabel(st.Board)
		if err != nil {
			return nil, fmt.Errorf("standup %s: %w", st.Key, err)
		}
		fx.Projects = append(fx.Projects, jiratest.FxProject{ID: strconv.Itoa(10001 + i), Key: st.Key, Name: st.Name})
		board := jiratest.FxBoard{
			ID: boardID, Name: boardName, Type: kind, ProjectKey: st.Key, FilterID: 9001 + i, Columns: sampleColumns(),
		}
		if kind == "scrum" {
			sp, err := parseSprint(st.Sprint)
			if err != nil {
				return nil, fmt.Errorf("standup %s: %w", st.Key, err)
			}
			board.Sprint = &sp
		}
		fx.Boards = append(fx.Boards, board)

		for _, p := range st.People {
			id := "u-" + p.ID
			if !seenUser[id] {
				seenUser[id] = true
				fx.Users = append(fx.Users, jiratest.FxUser{AccountID: id, DisplayName: p.Name})
			}
		}

		for _, tk := range st.Tickets {
			is, err := convertTicket(tk, boardID, kind == "scrum", &nextPR)
			if err != nil {
				return nil, fmt.Errorf("ticket %s: %w", tk.K, err)
			}
			fx.Issues = append(fx.Issues, is)
		}
		for _, b := range st.Backlog {
			typ, err := issueType(b.Ty)
			if err != nil {
				return nil, fmt.Errorf("backlog %s: %w", b.K, err)
			}
			fx.Backlog = append(fx.Backlog, jiratest.FxBacklog{Key: b.K, Type: typ, Summary: b.T, Board: boardID})
		}
	}
	return fx, nil
}

// sampleDoc is the subset of sample-data.json that Convert reads.
type sampleDoc struct {
	Standups []sampleStandup `json:"standups"`
}

type sampleStandup struct {
	Name   string `json:"name"`
	Key    string `json:"key"`
	Board  string `json:"board"`
	Sprint string `json:"sprint"`
	People []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"people"`
	Tickets []sampleTicket `json:"tickets"`
	Backlog []struct {
		K  string `json:"k"`
		Ty string `json:"ty"`
		T  string `json:"t"`
	} `json:"backlog"`
}

type sampleTicket struct {
	K    string  `json:"k"`
	A    *string `json:"a"`
	Ty   string  `json:"ty"`
	T    string  `json:"t"`
	Bl   bool    `json:"bl"`
	BlD  int     `json:"blD"`
	Ev   []int   `json:"ev"`
	Cm   []bool  `json:"cm"`
	Hist []struct {
		S string `json:"s"`
		D int    `json:"d"`
	} `json:"hist"`
}

// convertTicket applies the per-ticket rules. Day index i of ev/cm is day i−27 (day 0 = today).
func convertTicket(tk sampleTicket, boardID int, scrum bool, nextPR *int) (jiratest.FxIssue, error) {
	if len(tk.Ev) != 28 || len(tk.Cm) != 28 {
		return jiratest.FxIssue{}, fmt.Errorf("ev and cm must have 28 entries (have %d and %d)", len(tk.Ev), len(tk.Cm))
	}
	typ, err := issueType(tk.Ty)
	if err != nil {
		return jiratest.FxIssue{}, err
	}
	is := jiratest.FxIssue{
		Key: tk.K, Type: typ, Summary: tk.T, Board: boardID, InSprint: scrum,
		FlaggedDays: [][2]int{}, History: []jiratest.FxHistory{}, Commits: []int{},
		PRs: []jiratest.FxPR{}, Comments: []int{},
	}
	if tk.A != nil {
		is.Assignee = "u-" + *tk.A
	}
	if tk.Bl {
		is.FlaggedDays = append(is.FlaggedDays, [2]int{-(max(tk.BlD, 1) - 1), 0})
	}
	for _, h := range tk.Hist {
		name, err := laneStatus(h.S)
		if err != nil {
			return is, err
		}
		is.History = append(is.History, jiratest.FxHistory{Status: name, Days: h.D})
	}
	for i, v := range tk.Ev {
		day := i - 27
		switch v {
		case 1, 2:
			for range v {
				is.Commits = append(is.Commits, day)
			}
		case 3:
			is.PRs = append(is.PRs, jiratest.FxPR{ID: *nextPR, Day: day, Status: prStatus(day)})
			*nextPR++
		}
	}
	for i, c := range tk.Cm {
		if c {
			is.Comments = append(is.Comments, i-27)
		}
	}
	return is, nil
}

// prStatus is deterministic: recent pull requests are still open, older ones merged.
func prStatus(day int) string {
	if day >= -2 {
		return "OPEN"
	}
	return "MERGED"
}

var (
	boardLabelRe = regexp.MustCompile(`^(.*) · (\w+)$`)
	sprintDayRe  = regexp.MustCompile(`^day (\d+) of (\d+)$`)
)

// parseBoardLabel turns "MOB board · Scrum" into ("MOB board", "scrum").
func parseBoardLabel(label string) (name, kind string, err error) {
	m := boardLabelRe.FindStringSubmatch(label)
	if m == nil {
		return "", "", fmt.Errorf("board label %q has no type", label)
	}
	switch strings.ToLower(m[2]) {
	case "scrum":
		return m[1], "scrum", nil
	case "kanban":
		return m[1], "kanban", nil
	}
	return "", "", fmt.Errorf("board label %q has unknown type %q", label, m[2])
}

// parseSprint reads "Sprint 42 · ends today" or "Sprint 18 · day 6 of 10" into a fixture sprint. "ends today" is a
// 14-day sprint ending today; "day D of N" runs from day 1−D to day N−D.
func parseSprint(label string) (jiratest.FxSprint, error) {
	name, rest, _ := strings.Cut(label, " · ")
	num, ok := strings.CutPrefix(name, "Sprint ")
	if !ok {
		return jiratest.FxSprint{}, fmt.Errorf("sprint label %q has no sprint name", label)
	}
	id, err := strconv.Atoi(num)
	if err != nil {
		return jiratest.FxSprint{}, fmt.Errorf("sprint label %q has no sprint number", label)
	}
	sp := jiratest.FxSprint{ID: id, Name: name}
	if rest == "ends today" {
		sp.StartDay, sp.EndDay = -13, 0
		return sp, nil
	}
	m := sprintDayRe.FindStringSubmatch(rest)
	if m == nil {
		return jiratest.FxSprint{}, fmt.Errorf("sprint label %q has no schedule", label)
	}
	day, _ := strconv.Atoi(m[1])
	total, _ := strconv.Atoi(m[2])
	sp.StartDay, sp.EndDay = 1-day, total-day
	return sp, nil
}

func sampleColumns() []jiratest.FxColumn {
	names := []string{"To Do", "In Progress", "Code Review", "QA", "Done"}
	cols := make([]jiratest.FxColumn, 0, len(names))
	for _, n := range names {
		cols = append(cols, jiratest.FxColumn{Name: n, Statuses: []string{n}})
	}
	return cols
}

func issueType(ty string) (string, error) {
	switch ty {
	case "story":
		return "Story", nil
	case "bug":
		return "Bug", nil
	case "task":
		return "Task", nil
	case "spike":
		return "Spike", nil
	}
	return "", fmt.Errorf("unknown ticket type %q", ty)
}

// laneStatus maps a sample lane id to its status name (the lanes of sampleColumns).
func laneStatus(lane string) (string, error) {
	switch lane {
	case "todo":
		return "To Do", nil
	case "prog":
		return "In Progress", nil
	case "review":
		return "Code Review", nil
	case "qa":
		return "QA", nil
	case "done":
		return "Done", nil
	}
	return "", fmt.Errorf("unknown lane %q", lane)
}

func main() {
	in := flag.String("in", "design/reference/sample-data.json", "sample-data.json to convert")
	out := flag.String("out", "internal/source/jira/jiratest/testdata/fixture.json", "fixture.json to write")
	flag.Parse()
	if err := run(*in, *out); err != nil {
		fmt.Fprintln(os.Stderr, "fixturegen:", err)
		os.Exit(1)
	}
}

// run converts in and writes out atomically. Nothing is written when the conversion fails.
func run(in, out string) error {
	sample, err := os.ReadFile(in)
	if err != nil {
		return fmt.Errorf("read %s: %w", in, err)
	}
	fx, err := Convert(sample)
	if err != nil {
		return fmt.Errorf("convert %s: %w", in, err)
	}
	b, err := json.MarshalIndent(fx, "", "  ")
	if err != nil {
		return fmt.Errorf("encode fixture: %w", err)
	}
	b = append(b, '\n')
	tmp := out + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", out, err)
	}
	if err := os.Rename(tmp, out); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s: %w", out, err)
	}
	return nil
}
