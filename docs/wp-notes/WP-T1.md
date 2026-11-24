# WP-T1: ADF normalisation (Parse, PlainText, FromPlainText)

Files: `internal/adf/adf.go` (Parse, node normalisation, marks, media, cards, dates), `internal/adf/raw.go` (raw decode,
attribute helpers, URL and colour checks), `internal/adf/text.go` (PlainText, FromPlainText).

## Assumptions
- All-zero `Attrs` are stored as a nil `A` pointer, so no `"a":{}` is emitted.
- A non-object JSON root (for example `[]`) is malformed: one unknown node holding the raw text, `Unsupported=1`.
- A root object whose `type` is not `doc` is normalised as a single node.
- Each `decisionItem`'s content is wrapped in one paragraph, as the golden expects.
- `FromPlainText` mention offsets refer to the original text, taken before CRLF handling, so they stay correct when
  CRLF is present.
- `PlainText` renders unknown nodes as one line of their text; containers recurse; rules contribute nothing.
- `Parse` with a nil Resolver uses a no-op resolver and the wall clock.

## Test fix (orchestrator-authorised)
`internal/adf/adf_test.go` line 290 now calls `canon(t, []byte(tc.want))`. The `want` value is a Go string holding JSON,
and `canon` only parses `[]byte` as raw JSON.

## Checks
- `go test -tags purego -run=^$ -fuzz=FuzzParse -fuzztime=20s ./internal/adf/`: no crasher, about 6.5M execs.
- `CGO_ENABLED=0 go vet -tags purego ./...`, `scripts/layercheck`, `gofmt -l internal/adf`: clean.
- `CGO_ENABLED=0 go test -tags purego ./... -count=1`: all packages pass.
