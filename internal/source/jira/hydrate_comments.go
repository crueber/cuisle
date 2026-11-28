package jira

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/crueber/cuisle/internal/model"
)

// commentPageSize is maxResults of GET /rest/api/3/issue/{id}/comment (jira-sync.md §2.10).
const commentPageSize = 100

// wireCommentFull is one comment of the comment endpoint. The e-mail of an author is never decoded.
type wireCommentFull struct {
	ID         refID           `json:"id"`
	Created    Time            `json:"created"`
	Updated    Time            `json:"updated"`
	Author     *wireUser       `json:"author"`
	Body       json.RawMessage `json:"body"`
	Visibility *struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"visibility"`
}

// commentOf maps a comment; UpdatedAt equals At when the provider sends no update time.
func commentOf(wc wireCommentFull) model.Comment {
	c := model.Comment{ID: string(wc.ID), At: wc.Created.Time, UpdatedAt: wc.Updated.Time}
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = c.At
	}
	if wc.Author != nil {
		c.AuthorID = wc.Author.AccountID
	}
	if !isEmptyJSON(wc.Body) {
		c.Body = model.RichText{Format: model.RichTextADF, Raw: wc.Body}
	}
	if wc.Visibility != nil && (wc.Visibility.Type != "" || wc.Visibility.Value != "") {
		c.Visibility = &model.Visibility{Type: wc.Visibility.Type, Value: wc.Visibility.Value}
	}
	return c
}

// fetchComments reads the complete comment list of d, oldest first, by pages of 100 advancing startAt by the number
// of comments received, until startAt reaches the total or a page is empty. Authors are added to d.People.
func (a *Adapter) fetchComments(ctx context.Context, d *model.IssueDetail) error {
	path := "/rest/api/3/issue/" + url.PathEscape(d.SourceID) + "/comment"
	var out []model.Comment
	start, total := 0, 0
	for {
		var page struct {
			Total    int               `json:"total"`
			Comments []wireCommentFull `json:"comments"`
		}
		q := url.Values{
			"orderBy":    {"created"},
			"maxResults": {strconv.Itoa(commentPageSize)},
			"startAt":    {strconv.Itoa(start)},
		}
		if err := a.c.GetJSON(ctx, path, q, &page); err != nil {
			return MapError(err)
		}
		total = page.Total
		for _, wc := range page.Comments {
			out = append(out, commentOf(wc))
			if wc.Author != nil {
				addPerson(d, personOf(wc.Author))
			}
		}
		start += len(page.Comments)
		if len(page.Comments) == 0 || start >= total {
			break
		}
	}
	d.Comments = out
	d.CommentTotal = total
	return nil
}
