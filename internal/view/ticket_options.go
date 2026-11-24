package view

// Inputs of the ticket modal that the frozen BuildTicket signature has no slot for (WP-T6). cache.Ticket carries the
// connection id but neither the connected account, nor the source's capabilities, nor the connection state, and
// "the follow-up refresh failed" is known only to the caller; BuildTicketWith takes them. BuildTicket builds with the
// zero TicketOptions: nobody is "self", and no write is offered (Reason "read_only").

// TicketWrites are the light writes the source supports (source.Capabilities.Writes; view may not import source).
type TicketWrites struct {
	Transition bool
	Assign     bool
	Flag       bool
	Comment    bool
}

// TicketOptions are the caller's facts about the connection.
type TicketOptions struct {
	SelfAccountID string       // the connected account: PersonRef.Self and mention Self
	Writes        TicketWrites // what the source supports
	AuthState     string       // the connection state: "ok", "invalid", "missing", "unknown"; "" counts as ok
	RefreshFailed bool         // a write succeeded but its follow-up refresh failed: adds the refresh_failed warning

	// FieldsInUse is cache.FieldsInUseTx: the fields with a value on another issue of the same space. It bounds the Custom
	// group's Not set line: a non-pinned empty custom field is listed only when it is in this set. Pinned empty fields are
	// always listed. nil means "unknown" and lists none of the non-pinned ones.
	FieldsInUse map[string]bool
}

// CanFor is TicketDetail.Can: the source capability ANDed with a connection in state ok. A connection that is not ok
// offers nothing (Reason "auth"); a source with no write support offers nothing (Reason "read_only"). The frontend
// additionally disables writes while offline (Reason "offline" is its to set).
func CanFor(w TicketWrites, authState string) TicketCaps {
	if authState != "" && authState != "ok" {
		return TicketCaps{Reason: "auth"}
	}
	if !w.Transition && !w.Assign && !w.Flag && !w.Comment {
		return TicketCaps{Reason: "read_only"}
	}
	return TicketCaps{Transition: w.Transition, Assign: w.Assign, Flag: w.Flag, Comment: w.Comment}
}
