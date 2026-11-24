package cache

import (
	"encoding/json"
	"regexp"
	"sort"
)

// issueKeyRe finds provider issue keys (PROJ-123) inside document text and URLs.
var issueKeyRe = regexp.MustCompile(`[A-Z][A-Z0-9_]*-[0-9]+`)

// docRefs collects what a stored rich-text document names: the accounts it mentions, the issue keys it contains and
// the media ids of its (non-external) media nodes. Malformed input yields nothing. It parses only to read ids; the
// display tree is built later by internal/adf.
type docRefs struct {
	accounts map[string]bool
	keys     map[string]bool
	mediaIDs []string
}

func newDocRefs() *docRefs {
	return &docRefs{accounts: map[string]bool{}, keys: map[string]bool{}}
}

// add reads one document.
func (r *docRefs) add(raw []byte) {
	if len(raw) == 0 {
		return
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return
	}
	r.walk(v)
}

func (r *docRefs) walk(v any) {
	switch n := v.(type) {
	case map[string]any:
		attrs, _ := n["attrs"].(map[string]any)
		switch n["type"] {
		case "mention":
			if id, _ := attrs["id"].(string); id != "" {
				r.accounts[id] = true
			}
		case "media", "mediaInline":
			if id, _ := attrs["id"].(string); id != "" && attrs["type"] != "external" {
				r.mediaIDs = append(r.mediaIDs, id)
			}
		case "inlineCard", "blockCard", "embedCard":
			if u, _ := attrs["url"].(string); u != "" {
				for _, k := range issueKeyRe.FindAllString(u, -1) {
					r.keys[k] = true
				}
			}
		case "text":
			// links are marks on text nodes
			if marks, ok := n["marks"].([]any); ok {
				for _, m := range marks {
					mm, _ := m.(map[string]any)
					if mm["type"] != "link" {
						continue
					}
					ma, _ := mm["attrs"].(map[string]any)
					if href, _ := ma["href"].(string); href != "" {
						for _, k := range issueKeyRe.FindAllString(href, -1) {
							r.keys[k] = true
						}
					}
				}
			}
		}
		if cs, ok := n["content"].([]any); ok {
			for _, c := range cs {
				r.walk(c)
			}
		}
	case []any:
		for _, c := range n {
			r.walk(c)
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
