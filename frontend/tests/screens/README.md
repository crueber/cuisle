# Screenshot parity suite

`parity.spec.ts` renders the person view, whole board and Configure at the four mockup sizes (1920×1080, 1440×900,
1280×720, 1024×640) and a dark variant of person and board at 1920×1080. It runs in mock mode against the Go golden
bootstrap payload (`tests/fixtures/bootstrap_mob.json`). Time is frozen to the fixture's `generatedAt` + 60 s so the
header's "synced N min ago" is stable. Tolerance: `maxDiffPixelRatio: 0.02`, animations disabled (testing §5).

`zoom.spec.ts` (F15, ux-spec §16) shoots the whole board at 80 / 100 / 150 / 175 % and the person view at 150 % on
1920×1080 (compare with `design/mockups/zoom-*-1920x1080.png` by eye), and checks the 3 / 2 / 2 / 1 columns, the toast, the
header and the ticket modal never zooming, ⌘/Ctrl+ keys being prevented, scroll anchoring, and nothing scrolling sideways at
all nine levels at 1920×1080 and 1280×720 (also run it with `--browser=webkit`).

Mockups are a guide, not pixel law. Differences listed below are deliberate (ux-spec §13 or this build) unless marked
`DEFECT:`.

## Running

From `frontend/`:

```
./node_modules/.bin/vite build --mode mock --outDir dist-mock
./node_modules/.bin/vite preview --mode mock --outDir dist-mock --port 4186 --strictPort &
PLAYWRIGHT_BASE_URL=http://127.0.0.1:4186 ./node_modules/.bin/playwright test tests/screens
PLAYWRIGHT_BASE_URL=http://127.0.0.1:4186 ./node_modules/.bin/playwright test tests/screens --update-snapshots   # after a deliberate change
```

Baselines live in `tests/__screenshots__/screens/parity.spec.ts/` as `<name>.png`. They were generated on macOS
(Chromium). **Linux baselines are not committed yet:** they must be generated on the first Linux CI run with
`--update-snapshots`, reviewed, and committed. Font rendering differs between macOS and Linux, so do not compare one
platform's images against the other.

Note: `playwright.config.ts` sets `snapshotPathTemplate` without a `{platform}` token, so the file names carry no
platform suffix. Playwright does not allow a per-file override, so Linux CI currently reads the same files as macOS.
Add the platform suffix to the config's template (for example `{arg}{-snapshotSuffix}{ext}`) before the Linux
baselines are generated; this is a config change outside this suite.

## Mockup comparison

Baselines are at `tests/__screenshots__/screens/parity.spec.ts/`. Notes are the intended differences, checked by eye
against each mockup.

| Mockup (`design/mockups/`) | Baseline | Intended differences |
|---|---|---|
| `person-1920x1080.png` | `person-1920x1080.png` | Wordmark is CUISLE (ux §13 and the brand). One standup "New standup" in the fixture, so one tab (mockup has five). Header has the Resync button. Lane colours darkened for the §10 contrast (To Do bars darker). Fixture tickets and counts. |
| `person-1440x900.png` | `person-1440x900.png` | As above. Compact rail (below 1360 px the rail is the 76 px compact form, §13). Density label "tight rows" where the spec tightens before scrolling. |
| `person-1280x720.png` | `person-1280x720.png` | As above. Compact rail. Scroll bar "Scroll for more · 6 Done below" (§6.4). |
| `person-1024x640.png` | `person-1024x640.png` | As above. "Scroll for more" bar overlays the last rows (§6.4, nothing hidden). Density label "tight rows". |
| `board-1920x1080.png` | `board-1920x1080.png` | Fixture data (counts, tickets). Lane colours as above. The header reads "12 backlog" and the footer "20 more ranked below in Jira" (see DEFECT). |
| `board-1440x900.png` | `board-1440x900.png` | As board 1920. Done and Backlog sit below the fold behind the scroll bar. |
| `board-1280x720.png` | `board-1280x720.png` | As board 1440. Scroll bar "Done (15) · Backlog (12) below". |
| `board-1024x640.png` | `board-1024x640.png` | As board 1280. Scroll bar lists the lanes below the fold ("Silas · Noor · …"), which the mockup does not show (§6.4). |
| `configure-1920x1080.png` | `configure-1920x1080.png` | Autosave, so the header has **Done** and no Cancel or Save (ux §9, invariant 3). Three-column layout with no jump pills (as the mockup). Mockup's "~/.config" path line is not shown. |
| `configure-1440x900.png` | `configure-1440x900.png` | As configure 1920 but two columns, with jump pills (§9.1) above the form. |
| `configure-1280x720.png` | `configure-1280x720.png` | As configure 1440. Status and Export sections below the fold. |
| `configure-1024x640.png` | `configure-1024x640.png` | As configure 1440. Jump pills wrap onto two rows. Standup name wraps. |
| `board-case-team-1920x1080.png` | none | Not in this suite: needs the Web & Checkout standup selected. Add a case if this state needs a baseline. |
| `configure-new-standup-1920x1080.png` | none | Not in this suite: needs the new-standup form. |
| `person-unassigned-1920x1080.png` | none | Not in this suite: needs the Unassigned speaker selected. |
| (dark, no mockup) | `person-dark-1920x1080.png`, `board-dark-1920x1080.png` | Dark theme. No mockup. Checked against the light-theme layout only. |

### DEFECT list

Looked at, by eye, against the mockups. No clear unintended differences found; these need a second look rather than
a fix:

- DEFECT: whole board 1920 (and 1440/1280/1024): the header says "12 backlog" while the backlog footer says "Showing
  the top 12 … · 20 more ranked below in Jira". The header count and the footer disagree about the backlog total. The
  mockup header reads "40 backlog". Check which number the header should show.
- DEFECT (to verify): dark theme, board 1920: the active "Whole board" rail item is a light fill with dark text. The
  light theme inverts this (dark fill). Check that this is the intended dark-theme active state.
