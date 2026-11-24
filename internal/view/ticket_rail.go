package view

// The field rail of the ticket modal (ux-spec §14.6, architecture.md "Rail folding is done in Go"). A field with no value
// produces no row; its label goes to the group's NotSet. WP-T6; the SPEC-NOTEs of ticket_test.go state the rules where the
// docs leave room.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/model"
)

// pointsNameRe finds the story-points field among the number fields of the registry.
var pointsNameRe = regexp.MustCompile(`(?i)^story\s*points?(\s*estimate)?$`)

// railState carries what the group builders share.
type railState struct {
	b      *ticketBuilder
	vals   map[string]cache.TicketField    // non-empty values by field id
	reg    map[string]cache.TicketFieldDef // the connection's registry
	hidden map[string]bool
	points string // the story-points field id ("" = none)
}

func newRailState(b *ticketBuilder, prefs TicketFieldPrefs) *railState {
	rs := &railState{
		b: b, vals: map[string]cache.TicketField{}, reg: map[string]cache.TicketFieldDef{}, hidden: map[string]bool{},
	}
	for _, d := range b.t.Registry {
		rs.reg[d.Def.ID] = d
	}
	for _, f := range b.t.Fields {
		if !emptyValue(f.Value) {
			rs.vals[f.Value.FieldID] = f
		}
	}
	for _, h := range prefs.Hidden {
		rs.hidden[h.FieldID] = true
	}
	ids := make([]string, 0, len(rs.reg))
	for id := range rs.reg {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		d := rs.reg[id].Def
		if d.Custom && d.Kind == model.FieldNumber && pointsNameRe.MatchString(strings.TrimSpace(d.Name)) {
			rs.points = id
			break
		}
	}
	if rs.points == "" {
		for _, f := range b.t.Fields {
			if f.Custom && f.Value.Kind == model.FieldNumber && pointsNameRe.MatchString(strings.TrimSpace(f.Name)) {
				rs.points = f.Value.FieldID
				break
			}
		}
	}
	return rs
}

// emptyValue reports a field value with nothing to show.
func emptyValue(fv model.FieldValue) bool {
	switch fv.Kind {
	case model.FieldNumber:
		return fv.Number == nil
	case model.FieldOptions, model.FieldLabels, model.FieldVersions, model.FieldComponents, model.FieldGroups, model.FieldCascade:
		return len(fv.Items) == 0
	case model.FieldUser, model.FieldUsers:
		return len(fv.Users) == 0
	case model.FieldParagraph:
		return fv.Rich == nil || fv.Rich.IsEmpty()
	case model.FieldUnknown:
		s := strings.TrimSpace(string(fv.Raw))
		return s == "" || s == "null" || s == "{}" || s == "[]" || s == `""`
	}
	return fv.Text == ""
}

// rail builds the five groups in order (empty groups omitted) and the structured development summary.
func (b *ticketBuilder) rail(prefs TicketFieldPrefs, blockedDays int) ([]RailGroup, *DevSummary) {
	rs := newRailState(b, prefs)
	dev := b.devInfo()
	out := []RailGroup{}
	add := func(g RailGroup) {
		if len(g.Rows) > 0 || len(g.NotSet) > 0 {
			out = append(out, g)
		}
	}
	add(rs.state(blockedDays))
	add(rs.details())
	add(rs.custom(prefs))
	add(rs.development(dev))
	add(rs.dates())
	return out, dev.summary()
}

func newGroup(id, label string) RailGroup {
	return RailGroup{ID: id, Label: label, Rows: []RailRow{}, NotSet: []string{}}
}

func newRow(id, label, kind string) RailRow {
	return RailRow{FieldID: id, Label: label, Kind: kind, Items: []string{}, Users: []PersonRef{}}
}

// state is Status, Assignee and Flag: always three rows.
func (rs *railState) state(blockedDays int) RailGroup {
	b := rs.b
	g := newGroup(RailState, "State")

	st := newRow("status", "Status", RailKindStatus)
	st.Edit, st.Text = "status", b.statusName(b.t.Issue.StatusID)
	g.Rows = append(g.Rows, st)

	as := newRow("assignee", "Assignee", model.FieldUser)
	as.Edit = "assignee"
	if id := b.t.Issue.AssigneeID; id != "" {
		as.Users = append(as.Users, b.person(id))
	}
	g.Rows = append(g.Rows, as)

	fl := newRow("flagged", "Flag", RailKindFlag)
	fl.Edit, fl.Text = "flag", "Not flagged"
	if b.t.Issue.Flagged {
		fl.Text = "Flagged"
		if blockedDays > 0 {
			fl.Sub = strconv.Itoa(blockedDays) + "d"
		}
	}
	g.Rows = append(g.Rows, fl)
	return g
}

// dayText formats a date value ("2026-12-04") the way the rail shows it ("4 Dec 2026"); anything unparsable is kept.
func dayText(s string) string {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return s
	}
	return t.Format("2 Jan 2006")
}

// dateTimeText formats an RFC 3339 value in the viewer's zone ("4 Dec 2026 09:30"); anything unparsable is kept.
func (b *ticketBuilder) dateTimeText(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.In(b.asOf.Location()).Format("2 Jan 2006 15:04")
}

// readableKeys are the members of a provider object that name it, in order of preference.
var readableKeys = []string{"name", "value", "title", "displayName", "key"}

// readableRaw is a human reading of an unknown field value: the name-like member of an object, or the joined readings of
// an array's items, or a plain string, number or boolean. "" when nothing readable exists (the raw JSON is shown then).
func readableRaw(raw json.RawMessage) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	var read func(v any) string
	read = func(v any) string {
		switch x := v.(type) {
		case string:
			return strings.TrimSpace(x)
		case bool:
			return strconv.FormatBool(x)
		case float64:
			return strconv.FormatFloat(x, 'f', -1, 64)
		case map[string]any:
			for _, k := range readableKeys {
				if s, ok := x[k].(string); ok && strings.TrimSpace(s) != "" {
					return strings.TrimSpace(s)
				}
			}
		case []any:
			var parts []string
			for _, it := range x {
				if s := read(it); s != "" {
					parts = append(parts, s)
				}
			}
			if len(parts) == len(x) {
				return strings.Join(parts, ", ")
			}
		}
		return ""
	}
	return read(v)
}

// fieldRow renders a stored value by its kind (ux-spec §14.6 table).
func (rs *railState) fieldRow(label string, tf cache.TicketField) RailRow {
	fv := tf.Value
	r := newRow(fv.FieldID, label, fv.Kind)
	switch fv.Kind {
	case model.FieldURL:
		r.URL, r.URLLabel = fv.Text, urlLabel(fv.Text)
	case model.FieldNumber:
		r.Number, r.Mono = fv.Number, true
	case model.FieldDate:
		r.Text, r.Mono = dayText(fv.Text), true
	case model.FieldDateTime:
		r.Text, r.Mono = rs.b.dateTimeText(fv.Text), true
	case model.FieldOptions, model.FieldLabels, model.FieldComponents, model.FieldGroups, model.FieldCascade:
		r.Items = append(r.Items, fv.Items...)
	case model.FieldVersions:
		r.Items, r.Mono = append(r.Items, fv.Items...), true
	case model.FieldUser, model.FieldUsers:
		for _, id := range fv.Users {
			r.Users = append(r.Users, rs.b.person(id))
		}
	case model.FieldParagraph:
		if fv.Rich != nil {
			doc := rs.b.parse(*fv.Rich)
			r.Rich = &doc
		}
	case model.FieldUnknown:
		if t := readableRaw(fv.Raw); t != "" {
			r.Text = t // a provider object with a name, value, title, display name or key reads as that
		} else {
			r.Raw, r.Mono = strings.TrimSpace(string(fv.Raw)), true
		}
	case model.FieldVersion:
		r.Text, r.Mono = fv.Text, true
	default: // text, option, group, component, project, team
		r.Text = fv.Text
	}
	return r
}

// urlLabel is "host › last path segment" (the whole URL stays in the row's URL).
func urlLabel(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	seg := strings.Trim(u.Path, "/")
	if i := strings.LastIndex(seg, "/"); i >= 0 {
		seg = seg[i+1:]
	}
	if seg == "" {
		return u.Host
	}
	return u.Host + " › " + seg
}

// details is the Details group: reporter, priority, parent, sprint, points, due, labels, components, fix version, time,
// environment.
func (rs *railState) details() RailGroup {
	b := rs.b
	g := newGroup(RailDetails, "Details")
	// put adds the row, or the label to NotSet when the field applies but is empty. A hidden field is neither.
	put := func(id, label string, row *RailRow, applies bool) {
		if rs.hidden[id] {
			return
		}
		switch {
		case row != nil:
			g.Rows = append(g.Rows, *row)
		case applies:
			g.NotSet = append(g.NotSet, label)
		}
	}
	// stored fields: a row when a value exists; Not set when the site's registry has the field.
	stored := func(id, label string) {
		var row *RailRow
		if tf, ok := rs.vals[id]; ok {
			r := rs.fieldRow(label, tf)
			row = &r
		}
		_, inRegistry := rs.reg[id]
		put(id, label, row, inRegistry)
	}

	stored("reporter", "Reporter")
	stored("priority", "Priority")

	var parent *RailRow
	if p := b.t.Parent; p != nil {
		r := newRow("parent", "Parent", RailKindParent)
		r.Text, r.Mono = p.OtherKey, true
		r.Sub = p.OtherSummary
		if p.OtherIssueID != 0 && p.LiveSummary != "" {
			r.Sub = p.LiveSummary
		}
		parent = &r
	}
	put("parent", "Parent", parent, b.t.TypeKind != "epic")

	var sprint *RailRow
	if s, ok := rs.sprint(); ok {
		r := newRow("sprint", "Sprint", RailKindSprint)
		r.Text = s.Name
		if !s.EndAt.IsZero() {
			r.Sub = "ends " + s.EndAt.In(b.asOf.Location()).Format("2 Jan")
		}
		sprint = &r
	}
	put("sprint", "Sprint", sprint, b.snap.Config.BoardType == "scrum" && len(b.t.Issue.SprintIDs) == 0)

	if rs.points != "" {
		var row *RailRow
		if tf, ok := rs.vals[rs.points]; ok {
			r := rs.fieldRow("Points", tf)
			row = &r
		}
		put(rs.points, "Points", row, true)
	}

	stored("duedate", "Due")
	stored("labels", "Labels")
	stored("components", "Components")
	stored("fixVersions", "Fix version")

	var tt *RailRow
	if tf, ok := rs.vals["timetracking"]; ok {
		if r, ok := timeRow(tf); ok {
			tt = &r
		}
	}
	_, hasTT := rs.reg["timetracking"]
	put("timetracking", "Time", tt, hasTT)

	var env *RailRow
	if e := b.t.Environment; e != nil && !e.IsEmpty() {
		r := newRow("environment", "Environment", model.FieldParagraph)
		doc := b.parse(*e)
		r.Rich = &doc
		env = &r
	}
	put("environment", "Environment", env, true)
	return g
}

// sprint is the issue's sprint among the active ones the snapshot knows; the latest started wins.
func (rs *railState) sprint() (model.Sprint, bool) {
	in := map[string]bool{}
	for _, id := range rs.b.t.Issue.SprintIDs {
		in[id] = true
	}
	var best model.Sprint
	found := false
	for _, s := range rs.b.snap.Sprints {
		if in[s.ID] && (!found || !s.StartAt.Before(best.StartAt)) {
			best, found = s, true
		}
	}
	return best, found
}

// timeRow renders the time-tracking value (Jira's timetracking object kept as an unknown value).
func timeRow(tf cache.TicketField) (RailRow, bool) {
	var tt struct {
		Estimate int64 `json:"originalEstimateSeconds"`
		Spent    int64 `json:"timeSpentSeconds"`
	}
	if json.Unmarshal(tf.Value.Raw, &tt) != nil || (tt.Estimate <= 0 && tt.Spent <= 0) {
		return RailRow{}, false
	}
	r := newRow(tf.Value.FieldID, "Time", RailKindTime)
	r.Mono = true
	if tt.Estimate > 0 {
		r.Text = fmtDuration(tt.Spent) + " of " + fmtDuration(tt.Estimate)
		p := float64(tt.Spent) / float64(tt.Estimate)
		if p > 1 {
			p = 1
		}
		r.Progress = &p
	} else {
		r.Text = fmtDuration(tt.Spent)
	}
	return r, true
}

// fmtDuration renders seconds as "4h", "30m" or "1.5h".
func fmtDuration(secs int64) string {
	switch {
	case secs%3600 == 0:
		return strconv.FormatInt(secs/3600, 10) + "h"
	case secs < 3600:
		return strconv.FormatInt((secs+30)/60, 10) + "m"
	}
	h := float64(secs) / 3600
	return strings.TrimSuffix(fmt.Sprintf("%.1f", h), ".0") + "h"
}

// custom is the Custom fields group: pinned fields in pinned order (an empty one goes to NotSet), then the remaining
// non-hidden non-empty custom fields A-Z. The story-points field lives in Details.
func (rs *railState) custom(prefs TicketFieldPrefs) RailGroup {
	g := newGroup(RailCustom, "Custom fields")
	label := func(id string, tf cache.TicketField, fallback string) string {
		switch {
		case tf.Name != "":
			return tf.Name
		case rs.reg[id].Def.Name != "":
			return rs.reg[id].Def.Name
		case fallback != "":
			return fallback
		}
		return id
	}
	pinned := append([]FieldPref(nil), prefs.Pinned...)
	sort.SliceStable(pinned, func(i, j int) bool { return pinned[i].Position < pinned[j].Position })
	isPinned := map[string]bool{}
	for _, p := range pinned {
		if rs.hidden[p.FieldID] || p.FieldID == rs.points || isPinned[p.FieldID] {
			continue
		}
		isPinned[p.FieldID] = true
		if tf, ok := rs.vals[p.FieldID]; ok {
			g.Rows = append(g.Rows, rs.fieldRow(label(p.FieldID, tf, p.Name), tf))
		} else {
			g.NotSet = append(g.NotSet, label(p.FieldID, cache.TicketField{}, p.Name))
		}
	}

	type cand struct {
		id    string
		label string
		tf    cache.TicketField
	}
	var rest, empty []cand
	for id, tf := range rs.vals {
		if isPinned[id] || rs.hidden[id] || id == rs.points || !(tf.Custom || rs.reg[id].Def.Custom) {
			continue
		}
		rest = append(rest, cand{id: id, label: label(id, tf, ""), tf: tf})
	}
	// The registry drives Not set (cache.Ticket.Registry): every custom field of the connection that has no value on this
	// issue and do have one on another issue of the space (TicketOptions.FieldsInUse), except the ones the sync models
	// elsewhere (sprint, rank, flagged, development summary) and the story points. Pinned empty fields are listed above.
	for id, d := range rs.reg {
		if _, has := rs.vals[id]; has || isPinned[id] || rs.hidden[id] || id == rs.points || !d.Def.Custom || modelledElsewhere(d.Def) {
			continue
		}
		if !rs.b.opts.FieldsInUse[id] {
			continue // nobody else in the space ever filled it in: not worth a Not-set entry
		}
		empty = append(empty, cand{id: id, label: label(id, cache.TicketField{}, ""), tf: cache.TicketField{}})
	}
	byLabel := func(list []cand) {
		sort.Slice(list, func(i, j int) bool {
			a, b := strings.ToLower(list[i].label), strings.ToLower(list[j].label)
			if a != b {
				return a < b
			}
			return list[i].id < list[j].id
		})
	}
	byLabel(rest)
	byLabel(empty)
	for _, c := range rest {
		g.Rows = append(g.Rows, rs.fieldRow(c.label, c.tf))
	}
	for _, c := range empty {
		g.NotSet = append(g.NotSet, c.label)
	}
	return g
}

// modelledElsewhere is a custom field of the registry that never carries an issue_field_value because the sync models
// it elsewhere or treats it as noise (jira-sync.md §2.2): sprint, rank, flagged and the development summary.
func modelledElsewhere(d model.FieldDef) bool {
	switch d.CustomType {
	case "com.pyxis.greenhopper.jira:gh-sprint", "com.pyxis.greenhopper.jira:gh-lexo-rank":
		return true
	}
	return strings.HasSuffix(d.CustomType, ":devsummarycf") || strings.TrimSpace(d.Name) == "Flagged"
}

// dates is Created, Updated, Resolved and Watchers.
func (rs *railState) dates() RailGroup {
	b := rs.b
	g := newGroup(RailDates, "Dates")
	loc := b.asOf.Location()
	day := func(id, label string, t time.Time) {
		if rs.hidden[id] {
			return
		}
		if t.IsZero() {
			g.NotSet = append(g.NotSet, label)
			return
		}
		r := newRow(id, label, model.FieldDateTime)
		r.Text, r.Mono = t.In(loc).Format("2006-01-02"), true
		g.Rows = append(g.Rows, r)
	}
	day("created", "Created", b.t.Issue.CreatedAt)
	day("updated", "Updated", b.t.Issue.UpdatedAt)
	day("resolved", "Resolved", b.t.Issue.ResolvedAt)
	if b.t.Watchers >= 0 && !rs.hidden["watches"] {
		r := newRow("watches", "Watchers", RailKindWatchers)
		n := float64(b.t.Watchers)
		r.Number, r.Mono = &n, true
		g.Rows = append(g.Rows, r)
	}
	return g
}

// devData is the issue's development activity, reduced.
type devData struct {
	commits  int
	lastAt   time.Time
	prs      []devPR // newest update first
	branch   string  // the most recently updated pull request's source branch ("" = none known)
	asOf     time.Time
	disabled bool
}

type devPR struct {
	id, repo, status string
	title, branch    string // "" = not known (rows synced before migration 0004)
	at               time.Time
}

// devInfo reduces the dev events: commits (merge commits excluded) and each pull request's latest status.
func (b *ticketBuilder) devInfo() devData {
	d := devData{asOf: b.asOf, disabled: b.snap.DevActivity == "unavailable"}
	if d.disabled {
		return d
	}
	latest := map[string]devPR{}
	var order []string
	for _, e := range b.t.History.Dev {
		switch e.Kind {
		case model.DevCommit:
			if e.Merge {
				continue
			}
			d.commits++
			if e.At.After(d.lastAt) {
				d.lastAt = e.At
			}
		case model.DevPR:
			id := e.ID
			if i := strings.LastIndex(id, "#"); i >= 0 {
				id = id[i+1:]
			}
			cur, seen := latest[e.ID]
			if !seen {
				order = append(order, e.ID)
			}
			if !seen || !e.At.Before(cur.at) {
				next := devPR{id: id, repo: e.Repo, status: e.Status, title: e.Title, branch: e.Branch, at: e.At}
				// An older observation may know the title or branch a newer one lacks.
				if next.title == "" {
					next.title = cur.title
				}
				if next.branch == "" {
					next.branch = cur.branch
				}
				latest[e.ID] = next
			} else {
				if cur.title == "" {
					cur.title = e.Title
				}
				if cur.branch == "" {
					cur.branch = e.Branch
				}
				latest[e.ID] = cur
			}
		}
	}
	for _, k := range order {
		d.prs = append(d.prs, latest[k])
	}
	sort.SliceStable(d.prs, func(i, j int) bool { return d.prs[i].at.After(d.prs[j].at) })
	for _, p := range d.prs { // newest first: the first known branch is the most recent one
		if p.branch != "" {
			d.branch = p.branch
			break
		}
	}
	return d
}

func (d devData) empty() bool {
	return d.disabled || (d.commits == 0 && len(d.prs) == 0)
}

func (d devData) summary() *DevSummary {
	if d.empty() {
		return nil
	}
	s := &DevSummary{Branch: d.branch, Commits: d.commits, LastCommitAt: millisOrZero(d.lastAt), PRs: []DevPR{}}
	for _, p := range d.prs {
		s.PRs = append(s.PRs, DevPR{ID: p.id, Title: p.text(), Repo: p.repo, Status: p.status})
	}
	return s
}

// text is the pull request's title, or its repository for rows synced before the title was recorded.
func (p devPR) text() string {
	if p.title != "" {
		return p.title
	}
	return p.repo
}

// development is the branch row (the most recent pull request's source branch), the commits row and one row per pull
// request: its title, with the repository as the secondary text (the repository alone when the title is not known).
func (rs *railState) development(d devData) RailGroup {
	g := newGroup(RailDevelopment, "Development")
	if d.empty() {
		return g
	}
	if d.branch != "" && !rs.hidden["dev.branch"] {
		r := newRow("dev.branch", "Branch", RailKindBranch)
		r.Text, r.Mono = d.branch, true
		g.Rows = append(g.Rows, r)
	}
	if d.commits > 0 && !rs.hidden["dev.commits"] {
		r := newRow("dev.commits", "Commits", RailKindCommits)
		r.Text = strconv.Itoa(d.commits) + " commit"
		if d.commits != 1 {
			r.Text += "s"
		}
		r.Sub = "last " + age(d.asOf.Sub(d.lastAt))
		g.Rows = append(g.Rows, r)
	}
	for _, p := range d.prs {
		id := "dev.pr." + p.id
		if rs.hidden[id] {
			continue
		}
		r := newRow(id, "#"+p.id, RailKindPR)
		r.Text = p.text()
		if p.title != "" {
			r.Sub = p.repo
		}
		r.Items = append(r.Items, p.status)
		g.Rows = append(g.Rows, r)
	}
	return g
}

// age renders a duration as "just now", "20m", "2h" or "3d".
func age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	}
	return strconv.Itoa(int(d/(24*time.Hour))) + "d"
}
