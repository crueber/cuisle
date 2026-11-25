package view

// F14 command palette / ticket finder contract (docs/features/command-palette/HANDOFF.md, ux-spec §15). Types only: the
// matching (key and title) lives in internal/keyfind (pure) and internal/cache (candidates); the services are in
// internal/app.

// Tiers of a ticket hit (ux-spec §15.3).
const (
	TierExact  = "exact"  // the digits equal the issue number
	TierPrefix = "prefix" // the digits are a prefix of the issue number
	TierClose  = "close"  // one digit off, or two adjacent digits swapped
	TierTitle  = "title"  // every word of the query matches the summary (fuzzily), in any order
)

// TicketHit is one finder result. Lane, DaysInLane, OverLimit and Done are computed in the lane mapping of StandupID.
type TicketHit struct {
	IssueID          int64   `json:"issueId"`
	Key              string  `json:"key"`
	Summary          string  `json:"summary"`
	Type             string  `json:"type"` // story|bug|task|spike|subtask|epic|other
	Lane             string  `json:"lane"` // lane id in StandupID's mapping; "" when the status is unmapped there
	StatusName       string  `json:"statusName"`
	Assignee         string  `json:"assignee"` // accountId, "" = unassigned
	AssigneeInitials string  `json:"assigneeInitials"`
	DaysInLane       int     `json:"daysInLane"`
	OverLimit        bool    `json:"overLimit"`
	Done             bool    `json:"done"`
	Removed          bool    `json:"removed"`    // not a member of any standup now ("not on board")
	StandupID        int64   `json:"standupId"`  // the standup to open it in: the current one when a member, else the first member by tab order, else the current one
	StandupIDs       []int64 `json:"standupIds"` // every live standup that has it as a current member, in tab order
	Tier             string  `json:"tier"`       // exact|prefix|close|title
	Match            []int   `json:"match"`      // character indexes of Key to ink (exact/prefix: the matched digits; close: the equal digits); empty for a title hit
	TitleMatch       []int   `json:"titleMatch"` // rune indexes of Summary to ink (a title hit; empty for a key hit)
}

// TicketFind is the answer of TicketService.FindByKey.
type TicketFind struct {
	Hits       []TicketHit `json:"hits"`       // exact (all), prefix (first 10), close (first 5), then title (first 10); each tier in the §15.3 order
	PrefixMore int         `json:"prefixMore"` // starts-with matches beyond the 10 shown
	TitleMore  int         `json:"titleMore"`  // title matches beyond the 10 shown
}

// PersonStanding is one standup a person speaks in.
type PersonStanding struct {
	StandupID int64 `json:"standupId"`
	Active    int   `json:"active"` // active tickets (not done) they hold in that standup
}

// PersonHit is one `@` candidate (StandupService.People).
type PersonHit struct {
	AccountID   string           `json:"accountId"`
	DisplayName string           `json:"displayName"`
	Initials    string           `json:"initials"`
	Standups    []PersonStanding `json:"standups"` // tab order
}
