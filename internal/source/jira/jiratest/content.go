package jiratest

// Content and media of the fake (ADR 0015, jira-sync.md §6): the *all projection, comment bodies and visibility,
// attachments (303 to a pre-signed media URL), thumbnails and the media file endpoint. Content lives in the overlay
// testdata/content.json, keyed by issue key; every other issue gets generated content. Handlers run with the server
// lock held.

import (
	"bytes"
	"embed"
	"encoding/json"
	"image"
	_ "image/jpeg" // decodes the jpeg media the fake may serve
	"image/png"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

//go:embed testdata/content.json
var contentOverlayJSON []byte

//go:embed testdata/media
var mediaFiles embed.FS

// ── overlay (testdata/content.json) ─────────────────────────────────────────────────────────────

type ovOverlay struct {
	FieldDefs []json.RawMessage  `json:"fieldDefs"`
	Issues    map[string]ovIssue `json:"issues"`
}

type ovIssue struct {
	Fields         map[string]json.RawMessage `json:"fields"`
	DateFields     map[string]int             `json:"dateFields"`
	DateTimeFields map[string]int             `json:"dateTimeFields"`
	Attachments    []ovAttachment             `json:"attachments"`
	Comments       []ovComment                `json:"comments"`
	Links          []ovLink                   `json:"links"`
	Subtasks       []string                   `json:"subtasks"`
}

type ovAttachment struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	MimeType    string `json:"mimeType"`
	Author      string `json:"author"`
	Day         int    `json:"day"`
	File        string `json:"file"`
	MediaFileID string `json:"mediaFileId"`
}

type ovComment struct {
	Body       json.RawMessage `json:"body"`
	Visibility *ovVisibility   `json:"visibility"`
	EditedDay  *int            `json:"editedDay"`
}

type ovVisibility struct {
	Type       string `json:"type"`
	Value      string `json:"value"`
	Identifier string `json:"identifier"`
}

type ovLink struct {
	ID   string `json:"id"`
	Type struct {
		Name    string `json:"name"`
		Inward  string `json:"inward"`
		Outward string `json:"outward"`
	} `json:"type"`
	Direction string `json:"direction"`
	Key       string `json:"key"`
}

// overlay is parsed once; the file is part of the package, so a parse error is a build-time bug.
var overlay = func() ovOverlay {
	var o ovOverlay
	if err := json.Unmarshal(contentOverlayJSON, &o); err != nil {
		panic("jiratest: testdata/content.json: " + err.Error())
	}
	return o
}()

// findAttachment returns the overlay attachment with the given id.
func findAttachment(id string) (ovAttachment, bool) {
	for _, iss := range overlay.Issues {
		for _, a := range iss.Attachments {
			if a.ID == id {
				return a, true
			}
		}
	}
	return ovAttachment{}, false
}

// findMedia returns the overlay attachment whose media file id is uuid.
func findMedia(uuid string) (ovAttachment, bool) {
	for _, iss := range overlay.Issues {
		for _, a := range iss.Attachments {
			if a.MediaFileID == uuid {
				return a, true
			}
		}
	}
	return ovAttachment{}, false
}

// mediaBytes reads a file of testdata/media (nil when absent).
func mediaBytes(file string) []byte {
	b, err := mediaFiles.ReadFile("testdata/media/" + file)
	if err != nil {
		return nil
	}
	return b
}

// ── rendering (called with the lock held) ───────────────────────────────────────────────────

// dayAt is a local time on the day `day` days from the server's today.
func (s *Server) dayAt(day, hour, minute int) time.Time {
	loc := s.opts.Loc
	y, m, d := s.ds.Now.In(loc).Date()
	return time.Date(y, m, d+day, hour, minute, 0, 0, loc)
}

// adfDoc is a one-paragraph ADF document.
func adfDoc(text string) map[string]any {
	return map[string]any{"type": "doc", "version": 1, "content": []any{
		map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": text}}}}}
}

// issueJSON renders one issue with the requested fields (see projectFields).
func (s *Server) issueJSON(is *DSIssue, requested []string) map[string]any {
	return map[string]any{"id": is.ID, "key": is.Key, "fields": s.projectFields(is, requested)}
}

// projectFields applies a Jira field list: none, or "*all" (optionally with "-name" exclusions), gives every field;
// explicit names give only those.
func (s *Server) projectFields(is *DSIssue, requested []string) map[string]any {
	all := s.allFields(is)
	wantAll := false
	minus := map[string]bool{}
	var explicit []string
	for _, r := range requested {
		switch {
		case r == "*all":
			wantAll = true
		case strings.HasPrefix(r, "-"):
			minus[r[1:]] = true
		case r != "":
			explicit = append(explicit, r)
		}
	}
	out := map[string]any{}
	if wantAll || len(explicit) == 0 {
		for k, v := range all {
			if !minus[k] {
				out[k] = v
			}
		}
		return out
	}
	for _, k := range explicit {
		if v, ok := all[k]; ok {
			out[k] = v
		}
	}
	return out
}

// allFields is every field of the issue: the built-ins, the content, and the overlay values verbatim.
func (s *Server) allFields(is *DSIssue) map[string]any {
	ov := overlay.Issues[is.Key]
	f := map[string]any{
		"summary":        is.Summary,
		"issuetype":      s.issueTypeJSON(is.TypeID),
		"status":         s.statusJSON(is.StatusID),
		"assignee":       s.assigneeJSON(is.Assignee),
		"created":        jiraTime(is.Created),
		"updated":        jiraTime(is.Updated),
		"resolutiondate": optionalTime(is.Resolved),
		"parent":         nil,
		"comment":        commentJSON(is),
		SprintFieldID:    s.sprintsJSON(is),
		RankFieldID:      is.Rank,
		FlaggedFieldID:   flaggedJSON(is),
		"description":    adfDoc(is.Key + " description."),
		"attachment":     s.attachmentsJSON(ov),
		"issuelinks":     s.linksJSON(ov),
		"subtasks":       s.subtasksJSON(ov),
		"watches":        map[string]any{"self": "x", "watchCount": 0, "isWatching": false},
	}
	if !s.opts.NoDevField {
		f[DevSummaryFieldID] = devSummaryFieldValue(is)
	}
	for k, raw := range ov.Fields {
		f[k] = raw
	}
	for k, day := range ov.DateFields {
		y, m, d := s.ds.Now.In(s.opts.Loc).Date()
		f[k] = time.Date(y, m, d+day, 0, 0, 0, 0, s.opts.Loc).Format("2006-01-02")
	}
	for k, day := range ov.DateTimeFields {
		f[k] = jiraTime(s.dayAt(day, 9, 30))
	}
	return f
}

func (s *Server) attachmentsJSON(ov ovIssue) []any {
	out := []any{}
	for _, a := range ov.Attachments {
		m := map[string]any{
			"id": a.ID, "filename": a.Filename, "mimeType": a.MimeType, "size": len(mediaBytes(a.File)),
			"created": jiraTime(s.dayAt(a.Day, 9, 15)), "author": s.authorOf(a.Author),
			"content": s.URL + "/rest/api/3/attachment/content/" + a.ID,
		}
		if strings.HasPrefix(a.MimeType, "image/") {
			m["thumbnail"] = s.URL + "/rest/api/3/attachment/thumbnail/" + a.ID
		}
		out = append(out, m)
	}
	return out
}

func (s *Server) linksJSON(ov ovIssue) []any {
	out := []any{}
	for _, l := range ov.Links {
		other := s.issueByRef(l.Key)
		if other == nil {
			continue
		}
		m := map[string]any{"id": l.ID, "type": map[string]any{"name": l.Type.Name, "inward": l.Type.Inward, "outward": l.Type.Outward}}
		if l.Direction == "outward" {
			m["outwardIssue"] = s.refJSON(other)
		} else {
			m["inwardIssue"] = s.refJSON(other)
		}
		out = append(out, m)
	}
	return out
}

func (s *Server) subtasksJSON(ov ovIssue) []any {
	out := []any{}
	for _, k := range ov.Subtasks {
		if sub := s.issueByRef(k); sub != nil {
			out = append(out, s.refJSON(sub))
		}
	}
	return out
}

// refJSON is an issue reference: id, key and the summary, status and type ids.
func (s *Server) refJSON(is *DSIssue) map[string]any {
	return map[string]any{"id": is.ID, "key": is.Key, "fields": map[string]any{
		"summary": is.Summary, "status": s.statusJSON(is.StatusID), "issuetype": s.issueTypeJSON(is.TypeID),
	}}
}

// commentFullJSON renders comment i of the issue: body (overlay or generated ADF), visibility, and updated (the edited
// day at 15:00 local, else created).
func (s *Server) commentFullJSON(is *DSIssue, i int) map[string]any {
	c := is.Comments[i]
	ov := overlay.Issues[is.Key]
	body := any(adfDoc("Comment " + c.ID + " on " + is.Key + "."))
	updated := c.At
	m := map[string]any{"id": c.ID, "created": jiraTime(c.At), "author": s.authorOf(c.Author)}
	if i < len(ov.Comments) {
		oc := ov.Comments[i]
		if !isEmptyRaw(oc.Body) {
			body = oc.Body
		}
		if oc.Visibility != nil {
			vis := map[string]any{"type": oc.Visibility.Type, "value": oc.Visibility.Value}
			if oc.Visibility.Identifier != "" {
				vis["identifier"] = oc.Visibility.Identifier
			}
			m["visibility"] = vis
		}
		if oc.EditedDay != nil {
			updated = s.dayAt(*oc.EditedDay, 15, 0)
		}
	}
	m["body"] = body
	m["updated"] = jiraTime(updated)
	return m
}

func isEmptyRaw(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s == "" || s == "null"
}

// ── handlers ───────────────────────────────────────────────────────────────────────────────

// handleIssueGet serves GET /rest/api/3/issue/{id} (numeric id or key) with the fields query.
func (s *Server) handleIssueGet(w http.ResponseWriter, r *http.Request) {
	is := s.issueByRef(r.PathValue("id"))
	if is == nil {
		writeError(w, http.StatusNotFound, "Issue does not exist or you do not have permission to see it.")
		return
	}
	if s.f12RefreshFails(is) {
		writeError(w, http.StatusInternalServerError, "Injected refresh failure.")
		return
	}
	var req []string
	if f := r.URL.Query().Get("fields"); f != "" {
		req = strings.Split(f, ",")
	}
	writeJSON(w, http.StatusOK, s.issueJSON(is, req))
}

// handleAttachmentContent answers 303 to the pre-signed media URL of the attachment.
func (s *Server) handleAttachmentContent(w http.ResponseWriter, r *http.Request) {
	a, ok := findAttachment(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "Attachment does not exist or you do not have permission to see it.")
		return
	}
	w.Header().Set("Location", s.URL+"/media/file/"+a.MediaFileID+"/binary")
	w.WriteHeader(http.StatusSeeOther)
}

// handleThumbnail serves a PNG that fits inside width×height (default 320×200). Non-images have none (404).
func (s *Server) handleThumbnail(w http.ResponseWriter, r *http.Request) {
	a, ok := findAttachment(r.PathValue("id"))
	if !ok || !strings.HasPrefix(a.MimeType, "image/") {
		writeError(w, http.StatusNotFound, "Thumbnail does not exist.")
		return
	}
	img, _, err := image.Decode(bytes.NewReader(mediaBytes(a.File)))
	if err != nil {
		writeError(w, http.StatusNotFound, "Thumbnail does not exist.")
		return
	}
	bw := intQuery(r, "width", 320)
	bh := intQuery(r, "height", 200)
	var buf bytes.Buffer
	if err := png.Encode(&buf, fitBox(img, bw, bh)); err != nil {
		writeError(w, http.StatusInternalServerError, "Thumbnail could not be made.")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(buf.Bytes())
}

// handleMedia serves a media file by its uuid. It is pre-signed: no Authorization is required (see dispatch).
func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	a, ok := findMedia(r.PathValue("uuid"))
	if !ok {
		writeError(w, http.StatusNotFound, "Media does not exist.")
		return
	}
	w.Header().Set("Content-Type", a.MimeType)
	_, _ = w.Write(mediaBytes(a.File))
}

func intQuery(r *http.Request, key string, def int) int {
	n, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// fitBox scales src down (nearest neighbour, aspect kept) so it fits in w×h; smaller images are returned as they are.
func fitBox(src image.Image, w, h int) image.Image {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw <= w && sh <= h {
		return src
	}
	scale := math.Min(float64(w)/float64(sw), float64(h)/float64(sh))
	dw := max(1, int(float64(sw)*scale))
	dh := max(1, int(float64(sh)*scale))
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		sy := b.Min.Y + y*sh/dh
		for x := 0; x < dw; x++ {
			dst.Set(x, y, src.At(b.Min.X+x*sw/dw, sy))
		}
	}
	return dst
}
