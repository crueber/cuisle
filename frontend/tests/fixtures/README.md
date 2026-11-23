# Screenshot fixtures

`bootstrap_mob.json` is a copy of `internal/app/testdata/bootstrap_mob.golden.json` (the Go bootstrap payload for the
MOB standup). It is regenerated from the Go golden, never edited by hand.

To refresh it:

1. `go test -tags purego ./internal/app -run TestGolden -update-golden` (review the diff).
2. Copy `internal/app/testdata/bootstrap_mob.golden.json` over `frontend/tests/fixtures/bootstrap_mob.json`.
3. From `frontend/`, run `playwright test tests/screens --update-snapshots` and review the new baselines.
