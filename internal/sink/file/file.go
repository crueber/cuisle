// Package file is the v1 reference sink: writes the export JSON into a user-chosen folder, atomically
// (architecture.md §2.3). Implementation: WP-21.
package file

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/sink"
)

const Kind = "file"

const defaultName = "cuisle-{standupKey}-{date}.json"

// maxSuffix bounds the "-2", "-3"… search for overwrite=false.
const maxSuffix = 1000

type fileSink struct{}

// New returns the file sink. Config: dir (path, required), filename (text, default
// "cuisle-{standupKey}-{date}.json"), overwrite (bool, default "true"). The filename is cleaned and must not escape
// dir; writes go to a temp file in dir then rename; Receipt.Ref is the absolute path. overwrite=false appends
// "-2", "-3"… before the extension.
func New() sink.Sink { return fileSink{} }

func (fileSink) Kind() string        { return Kind }
func (fileSink) DisplayName() string { return "JSON file" }

func (fileSink) Capabilities() sink.Capabilities {
	return sink.Capabilities{Current: true, Formats: []string{"json"}, NeedsSecret: false, Idempotent: false}
}

func (fileSink) ConfigSchema() []sink.Field {
	return []sink.Field{
		{Key: "dir", Label: "Folder", Help: "Where the JSON file is written.", Type: "path", Required: true},
		{Key: "filename", Label: "File name", Help: "{standupKey} and {date} are filled in.", Type: "text",
			Default: defaultName},
		{Key: "overwrite", Label: "Overwrite", Help: "Replace an existing file, or add -2, -3… to the name.",
			Type: "bool", Default: "true"},
	}
}

func (fileSink) Validate(cfg map[string]string) error {
	if strings.TrimSpace(cfg["dir"]) == "" {
		return apperr.Validationf("dir", "Choose a folder.")
	}
	if !filepath.IsAbs(strings.TrimSpace(cfg["dir"])) {
		return apperr.Validationf("dir", "Choose a full folder path.")
	}
	if err := checkOverwrite(cfg["overwrite"]); err != nil {
		return err
	}
	if fn := cfg["filename"]; fn != "" {
		if filepath.IsAbs(fn) || strings.ContainsAny(fn, `/\:`+"\x00") || fn == "." || fn == ".." {
			return apperr.Validationf("filename", "The file name must not contain folders.")
		}
	}
	return nil
}

// checkOverwrite accepts an absent value (true), "true" or "false".
func checkOverwrite(v string) error {
	switch v {
	case "", "true", "false":
		return nil
	}
	return apperr.Validationf("overwrite", "Overwrite must be true or false.")
}

func (fileSink) Deliver(ctx context.Context, d sink.Delivery) (sink.Receipt, error) {
	if err := ctx.Err(); err != nil {
		return sink.Receipt{}, err
	}
	dir := strings.TrimSpace(d.Config["dir"])
	if dir == "" {
		return sink.Receipt{}, apperr.Validationf("dir", "Choose a folder.")
	}
	if !filepath.IsAbs(dir) {
		return sink.Receipt{}, apperr.Validationf("dir", "Choose a full folder path.")
	}
	if err := checkOverwrite(d.Config["overwrite"]); err != nil {
		return sink.Receipt{}, err
	}
	overwrite := d.Config["overwrite"] != "false"
	name, err := render(d.Config["filename"], d.StandupKey, d.Date)
	if err != nil {
		return sink.Receipt{}, err
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return sink.Receipt{}, apperr.Wrap(apperr.SinkFailed, "The folder could not be used.", err)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return sink.Receipt{}, apperr.Wrap(apperr.SinkFailed, "The folder does not exist.", err)
	}
	target, err := pickTarget(dir, name, overwrite)
	if err != nil {
		return sink.Receipt{}, err
	}
	if err := writeAtomic(dir, target, d.Raw); err != nil {
		return sink.Receipt{}, err
	}
	return sink.Receipt{Ref: target, Detail: fmt.Sprintf("%d bytes", len(d.Raw))}, nil
}

// render fills the filename template. A substituted value that carries a path separator is rejected (no folders can
// come from data), and the result must be a plain file name (not "." or "..").
func render(tmpl, standupKey, date string) (string, error) {
	if strings.TrimSpace(tmpl) == "" {
		tmpl = defaultName
	}
	if strings.ContainsAny(standupKey+date, `/\:`+"\x00") {
		return "", apperr.Validationf("filename", "The standup key or date contains a folder separator.")
	}
	name := strings.NewReplacer("{standupKey}", standupKey, "{date}", date).Replace(tmpl)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\:`+"\x00") || filepath.IsAbs(name) {
		return "", apperr.Validationf("filename", "The file name must not contain folders.")
	}
	return name, nil
}

// pickTarget returns dir/name, or with overwrite=false the first free name-N.ext.
func pickTarget(dir, name string, overwrite bool) (string, error) {
	target := filepath.Join(dir, name)
	if overwrite {
		return target, nil
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for n := 1; n <= maxSuffix; n++ {
		candidate := target
		if n > 1 {
			candidate = filepath.Join(dir, fmt.Sprintf("%s-%d%s", stem, n, ext))
		}
		_, err := os.Lstat(candidate)
		if os.IsNotExist(err) {
			return candidate, nil
		}
		if err != nil {
			return "", apperr.Wrap(apperr.SinkFailed, "The folder could not be read.", err)
		}
	}
	return "", apperr.New(apperr.SinkFailed, "No free file name was found.")
}

// writeAtomic writes data to a temp file in dir, then renames it over target. The temp file never outlives the call.
func writeAtomic(dir, target string, data []byte) (err error) {
	tmp, err := os.CreateTemp(dir, ".cuisle-*.tmp")
	if err != nil {
		return apperr.Wrap(apperr.SinkFailed, "The file could not be written.", err)
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return apperr.Wrap(apperr.SinkFailed, "The file could not be written.", err)
	}
	if err = tmp.Sync(); err != nil {
		return apperr.Wrap(apperr.SinkFailed, "The file could not be written.", err)
	}
	if err = tmp.Chmod(0o644); err != nil { // CreateTemp makes 0600; exports must be readable by other tools
		return apperr.Wrap(apperr.SinkFailed, "The file could not be written.", err)
	}
	if err = tmp.Close(); err != nil {
		return apperr.Wrap(apperr.SinkFailed, "The file could not be written.", err)
	}
	if err = os.Rename(tmp.Name(), target); err != nil {
		return apperr.Wrap(apperr.SinkFailed, "The file could not be written.", err)
	}
	return nil
}
