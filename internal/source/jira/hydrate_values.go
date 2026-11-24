package jira

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/crueber/cuisle/internal/model"
)

// Value decoding by registry kind (jira-sync.md §2.2 "Value decoding by kind"). A value of the wrong JSON shape for its
// kind is kept as kind unknown with the raw value; a value with nothing displayable is "not set" (no FieldValue).
// Values are never logged or put into errors.

// isEmptyJSON reports the values that mean "not set": absent, null, "", [] and {}.
func isEmptyJSON(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	switch s {
	case "", "null", `""`, "[]", "{}":
		return true
	}
	return false
}

// jsonObject decodes a JSON object into its raw members.
func jsonObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return nil, false
	}
	return m, true
}

// jsonStr reads a string member; anything else is "".
func jsonStr(m map[string]json.RawMessage, key string) string {
	var s string
	if err := json.Unmarshal(m[key], &s); err != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

// displayOf is the displayable name of an option-like object: value, else name.
func displayOf(m map[string]json.RawMessage) string {
	if v := jsonStr(m, "value"); v != "" {
		return v
	}
	return jsonStr(m, "name")
}

// unknownValue keeps a value verbatim.
func unknownValue(id string, raw json.RawMessage) (model.FieldValue, bool) {
	return model.FieldValue{FieldID: id, Kind: model.FieldUnknown, Raw: raw}, true
}

// decodeFieldValue decodes one provider value of the given kind. People found in user fields are added to d once.
func decodeFieldValue(d *model.IssueDetail, id, kind string, raw json.RawMessage) (model.FieldValue, bool) {
	if isEmptyJSON(raw) {
		return model.FieldValue{}, false
	}
	fv := model.FieldValue{FieldID: id, Kind: kind}
	switch kind {
	case model.FieldText, model.FieldURL, model.FieldDate:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return unknownValue(id, raw)
		}
		fv.Text = strings.TrimSpace(s)
		return fv, fv.Text != ""
	case model.FieldDateTime:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return unknownValue(id, raw)
		}
		var t Time
		if err := t.UnmarshalJSON(raw); err == nil && !t.IsZero() {
			fv.Text = t.UTC().Format(time.RFC3339Nano)
		} else {
			fv.Text = strings.TrimSpace(s)
		}
		return fv, fv.Text != ""
	case model.FieldParagraph:
		if _, ok := jsonObject(raw); !ok {
			return unknownValue(id, raw)
		}
		fv.Rich = &model.RichText{Format: model.RichTextADF, Raw: raw}
		return fv, true
	case model.FieldNumber:
		var f float64
		if err := json.Unmarshal(raw, &f); err != nil {
			return unknownValue(id, raw)
		}
		fv.Number = &f
		return fv, true
	case model.FieldOption, model.FieldGroup, model.FieldVersion, model.FieldProject, model.FieldTeam:
		m, ok := jsonObject(raw)
		if !ok {
			return unknownValue(id, raw)
		}
		fv.Text = displayOf(m)
		if fv.Text == "" && kind == model.FieldGroup {
			fv.Text = jsonStr(m, "name")
		}
		if fv.Text == "" && kind == model.FieldTeam {
			fv.Text = jsonStr(m, "title")
		}
		if fv.Text == "" && (kind == model.FieldGroup || kind == model.FieldVersion || kind == model.FieldProject) {
			fv.Text = jsonStr(m, "name")
		}
		return fv, fv.Text != ""
	case model.FieldOptions, model.FieldVersions, model.FieldComponents, model.FieldGroups, model.FieldLabels:
		var arr []json.RawMessage
		if err := json.Unmarshal(raw, &arr); err != nil {
			return unknownValue(id, raw)
		}
		for _, el := range arr {
			var s string
			if json.Unmarshal(el, &s) == nil {
				if s = strings.TrimSpace(s); s != "" {
					fv.Items = append(fv.Items, s)
				}
				continue
			}
			if m, ok := jsonObject(el); ok {
				if s := displayOf(m); s != "" {
					fv.Items = append(fv.Items, s)
				}
			}
		}
		return fv, len(fv.Items) > 0
	case model.FieldCascade:
		m, ok := jsonObject(raw)
		if !ok {
			return unknownValue(id, raw)
		}
		parent := displayOf(m)
		if parent == "" {
			return fv, false
		}
		fv.Items = []string{parent}
		if child, ok := jsonObject(m["child"]); ok {
			if c := displayOf(child); c != "" {
				fv.Items = append(fv.Items, c)
			}
		}
		return fv, true
	case model.FieldUser:
		var u wireUser
		if err := json.Unmarshal(raw, &u); err != nil {
			return unknownValue(id, raw)
		}
		if u.AccountID == "" {
			return fv, false
		}
		addPerson(d, personOf(&u))
		fv.Users = []string{u.AccountID}
		return fv, true
	case model.FieldUsers:
		var us []wireUser
		if err := json.Unmarshal(raw, &us); err != nil {
			return unknownValue(id, raw)
		}
		for i := range us {
			if us[i].AccountID == "" {
				continue
			}
			addPerson(d, personOf(&us[i]))
			fv.Users = append(fv.Users, us[i].AccountID)
		}
		return fv, len(fv.Users) > 0
	}
	return unknownValue(id, raw)
}
