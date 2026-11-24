package view

// The adf.Resolver over a loaded ticket (architecture.md §2.4, jira-sync.md §2.14 "Media-node → attachment mapping").
// It never touches the network and reads only cache.Ticket. WP-T6.

import (
	"encoding/json"
	"regexp"
	"sort"
	"time"

	"github.com/crueber/cuisle/internal/adf"
	"github.com/crueber/cuisle/internal/model"
)

// resolverKeyRe finds an issue key in a card URL (…/browse/MOB-12) or a bare key.
var resolverKeyRe = regexp.MustCompile(`[A-Z][A-Z0-9_]*-[0-9]+`)

// ticketResolver resolves one document. positional is the step-4 mapping (the n-th unresolved media node to the n-th
// remaining attachment), computed per document.
type ticketResolver struct {
	b          *ticketBuilder
	positional map[string]int // media id → index into b.t.Attachments
}

func newTicketResolver(b *ticketBuilder, raw model.RichText) *ticketResolver {
	r := &ticketResolver{b: b, positional: map[string]int{}}
	r.positional = r.positionalMap(mediaNodes(raw.Raw))
	return r
}

// mediaNode is one media node of a document, in document order.
type mediaNode struct{ id, alt string }

// mediaNodes lists the non-external media nodes of a document (media and mediaInline carry attrs.id).
func mediaNodes(raw []byte) []mediaNode {
	var root any
	if json.Unmarshal(raw, &root) != nil {
		return nil
	}
	var out []mediaNode
	var walk func(v any)
	walk = func(v any) {
		switch n := v.(type) {
		case map[string]any:
			if typ, _ := n["type"].(string); typ == "media" || typ == "mediaInline" {
				if a, _ := n["attrs"].(map[string]any); a != nil && a["type"] != "external" {
					id, _ := a["id"].(string)
					alt, _ := a["alt"].(string)
					if id != "" {
						out = append(out, mediaNode{id: id, alt: alt})
					}
				}
			}
			if cs, ok := n["content"].([]any); ok {
				for _, c := range cs {
					walk(c)
				}
			}
		case []any:
			for _, c := range n {
				walk(c)
			}
		}
	}
	walk(root)
	return out
}

// byID is the attachment whose media id equals id (step 1).
func (r *ticketResolver) byID(id string) (int, bool) {
	if id == "" {
		return 0, false
	}
	for i, a := range r.b.t.Attachments {
		if a.MediaFileID == id {
			return i, true
		}
	}
	return 0, false
}

// byName is the attachment whose name equals alt, when exactly one does (step 3).
func (r *ticketResolver) byName(alt string) (int, bool) {
	if alt == "" {
		return 0, false
	}
	found := -1
	for i, a := range r.b.t.Attachments {
		if a.Name == alt {
			if found >= 0 {
				return 0, false
			}
			found = i
		}
	}
	return found, found >= 0
}

// positionalMap is step 4: when the count of media nodes that steps 1 and 3 cannot place equals the count of
// attachments they did not place, the n-th such node maps to the n-th such attachment by creation time.
func (r *ticketResolver) positionalMap(nodes []mediaNode) map[string]int {
	used := map[int]bool{}
	var unresolved []mediaNode
	for _, n := range nodes {
		if i, ok := r.byID(n.id); ok {
			used[i] = true
			continue
		}
		if i, ok := r.byName(n.alt); ok {
			used[i] = true
			continue
		}
		unresolved = append(unresolved, n)
	}
	var rest []int
	for i := range r.b.t.Attachments {
		if !used[i] {
			rest = append(rest, i)
		}
	}
	out := map[string]int{}
	if len(unresolved) == 0 || len(unresolved) != len(rest) {
		return out
	}
	sort.SliceStable(rest, func(x, y int) bool {
		return r.b.t.Attachments[rest[x]].CreatedAt.Before(r.b.t.Attachments[rest[y]].CreatedAt)
	})
	for k, n := range unresolved {
		if _, dup := out[n.id]; !dup {
			out[n.id] = rest[k]
		}
	}
	return out
}

// Attachment implements adf.Resolver.
func (r *ticketResolver) Attachment(mediaFileID, alt string) (model.Attachment, string, bool) {
	i, ok := r.byID(mediaFileID)
	if !ok {
		i, ok = r.byName(alt)
	}
	if !ok {
		i, ok = r.positional[mediaFileID]
	}
	if !ok {
		r.b.unresolvedMedia = true
		return model.Attachment{}, adf.CachedNone, false
	}
	a := r.b.t.Attachments[i]
	return a.Attachment, cachedState(a.HasFile, a.HasThumb), true
}

// cachedState is the Cached value of an attachment: the original, else the thumbnail, else none.
func cachedState(hasFile, hasThumb bool) string {
	switch {
	case hasFile:
		return adf.CachedFull
	case hasThumb:
		return adf.CachedThumb
	}
	return adf.CachedNone
}

// Issue implements adf.Resolver: a synced issue named by a card URL or a bare key.
func (r *ticketResolver) Issue(url string) (adf.ResolvedIssue, bool) {
	key := resolverKeyRe.FindString(url)
	if key == "" {
		return adf.ResolvedIssue{}, false
	}
	b := r.b
	if key == b.t.Issue.Key {
		return adf.ResolvedIssue{
			IssueID: b.t.LocalID, Key: key, Summary: b.t.Issue.Summary, TypeKind: b.t.TypeKind,
			StatusName: b.statusName(b.t.Issue.StatusID), StatusLozenge: b.lane(b.t.Issue.StatusID),
		}, true
	}
	ref, ok := b.t.Issues[key]
	if !ok {
		return adf.ResolvedIssue{}, false
	}
	return adf.ResolvedIssue{
		IssueID: ref.LocalID, Key: ref.Key, Summary: ref.Summary, TypeKind: ref.TypeKind,
		StatusName: b.statusName(ref.StatusID), StatusLozenge: b.lane(ref.StatusID),
	}, true
}

// Person implements adf.Resolver.
func (r *ticketResolver) Person(accountID string) (string, bool) {
	name := r.b.people[accountID].DisplayName
	return name, accountID != "" && accountID == r.b.opts.SelfAccountID
}

// Now implements adf.Resolver.
func (r *ticketResolver) Now() time.Time { return r.b.asOf }
