// Converts design/reference/sample-data.json into the frontend mock fixtures (WP-14, architecture §4.4).
//
//   node scripts/mockfixture/generate.mjs        (from the repo root; deterministic, no network)
//
// Output: frontend/src/api/fixtures/{config.json, catalog.json, app.json, view_<key>.json}.
// The views are the plain-browser stand-in for Go's view.Build output: they follow jira-sync.md §5 (day indexes over a
// 28-day window, status segments from the per-lane day counts, marks from the sample's ev/cm arrays, blocked spans from
// bl/blD, track label composed like §5.8). The Go golden replaces view_mob.json later; keep the shapes identical.
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, "../..");
const sample = JSON.parse(readFileSync(resolve(root, "design/reference/sample-data.json"), "utf8"));
const outDir = resolve(root, "frontend/src/api/fixtures");

const DAY = 86_400_000;
const DAYS = 28;
const TODAY = "2026-11-23";
const TODAY_UTC = Date.UTC(2026, 10, 23);
/** The mock clock: 09:00 UTC on the sample's day. Every timestamp in the fixtures derives from it. */
const NOW = TODAY_UTC + 9 * 3_600_000;
const END_OF_TODAY = TODAY_UTC + 17 * 3_600_000;
const BACKLOG_ROWS = 12;

const WALK = ["qa", "review", "prog", "todo"]; // default "right to left" (product-spec walk order)
const LANE_LABEL = { todo: "To Do", prog: "In Progress", review: "In Review", qa: "QA", done: "Done" };
const LANE_STATUS = { todo: "To Do", prog: "In Progress", review: "Review", qa: "QA", done: "Done" };
const LIMIT = { todo: 10, prog: 5, review: 2, qa: 3, done: 0 }; // settings.DefaultLanes

const dateMs = (i) => TODAY_UTC - (DAYS - 1 - i) * DAY;
const dateOf = (i) => new Date(dateMs(i)).toISOString().slice(0, 10);
const dayStartMs = (i) => dateMs(i) + 9 * 3_600_000;
const plural = (n, one, many) => `${n} ${n === 1 ? one : many}`;

function fail(msg) {
  throw new Error(`mockfixture: ${msg}`);
}

// ── axis ──
const axis = sample.axis.map((a, i) => {
  const date = dateOf(i);
  const day = new Date(dateMs(i));
  const dow = day.getUTCDay();
  const out = { date, label: String(day.getUTCDate()), weekend: dow === 0 || dow === 6, monday: dow === 1 };
  if (out.label !== a.lab || out.weekend !== a.we || out.monday !== a.mon) fail(`axis ${i} disagrees with the sample`);
  return out;
});
if (axis.length !== DAYS || axis[DAYS - 1].date !== TODAY) fail("sample axis must be 28 days ending today");

// ── shared lane and board columns ──
const columnsFor = (lane) => [LANE_STATUS[lane]];
const boardColumns = [
  ...["todo", "prog", "review", "qa", "done"].map((lane) => ({
    columnName: LANE_STATUS[lane], statusNames: [LANE_STATUS[lane]], lane, auto: true,
  })),
  { columnName: "Backlog", statusNames: ["Backlog"], lane: "backlog", auto: true },
];
const laneInfos = () => [
  ...WALK.map((lane) => ({ lane, label: LANE_LABEL[lane], limitDays: LIMIT[lane], columns: columnsFor(lane) })),
  { lane: "done", label: LANE_LABEL.done, limitDays: 0, columns: columnsFor("done") },
];

// ── pulse (jira-sync.md §5) ──
function segmentsOf(hist) {
  const segs = [];
  let end = DAYS;
  for (let i = hist.length - 1; i >= 0 && end > 0; i--) {
    const from = Math.max(0, end - hist[i].d);
    if (end > from) segs.unshift({ lane: hist[i].s, from, to: end });
    end = from;
  }
  // A sum shorter than the window means the ticket was created inside it: the first segment starts at its created day.
  return segs;
}

function buildPulse(t, segs) {
  const start = segs[0].from;
  const marks = [];
  for (let k = start; k < DAYS; k++) {
    const ev = t.ev[k] ?? 0;
    const commits = ev === 1 ? 1 : ev === 2 ? 2 : 0;
    const prs = ev === 3 ? 1 : 0;
    const comments = t.cm[k] ? 1 : 0;
    if (commits + prs + comments > 0) marks.push({ day: k, commits, prs, comments });
  }
  const blocked = t.bl && t.blD > 0 ? [{ from: Math.max(0, DAYS - t.blD), to: DAYS - 1 }] : [];
  const prev = segs.length >= 2 ? segs[segs.length - 2] : null;
  const prevClause = prev ? `, previously ${LANE_LABEL[prev.lane]} ${plural(prev.to - prev.from, "day", "days")}` : "";
  const cur = segs[segs.length - 1];
  let label;
  if (cur.lane === "done") {
    label = `Done ${plural(t.days, "day", "days")} ago${prevClause}`;
  } else {
    const c = marks.reduce((a, m) => a + m.commits, 0);
    const p = marks.reduce((a, m) => a + m.prs, 0);
    const m = marks.reduce((a, x) => a + x.comments, 0);
    const parts = [];
    if (c > 0) parts.push(plural(c, "commit", "commits"));
    if (p > 0) parts.push(plural(p, "PR update", "PR updates"));
    if (m > 0) parts.push(plural(m, "comment", "comments"));
    const activity = parts.length ? `${parts.join(", ")} in ${DAYS} days` : `no activity in ${DAYS} days`;
    const blockedDays = blocked.reduce((a, b) => a + (b.to - b.from + 1), 0);
    label = `${LANE_LABEL[cur.lane]} ${plural(t.days, "day", "days")}${prevClause}; ${activity}`;
    if (blockedDays > 0) label += `; blocked ${plural(blockedDays, "day", "days")}`;
  }
  return { segs, marks, blocked, label };
}

// ── sprint and board header ──
function sprintOf(text, idx) {
  const m = /^(Sprint (\d+)) · (.*)$/.exec(text);
  if (!m) fail(`unparsed sprint header: ${text}`);
  const rest = m[3];
  let daysLeft;
  if (rest === "ends today") daysLeft = 0;
  else {
    const d = /^day (\d+) of (\d+)$/.exec(rest);
    if (!d) fail(`unparsed sprint state: ${rest}`);
    daysLeft = Number(d[2]) - Number(d[1]);
  }
  const endAt = END_OF_TODAY + daysLeft * DAY;
  const endsLabel = daysLeft === 0 ? "ends today" : `ends in ${plural(daysLeft, "day", "days")}`;
  return {
    id: `s-${m[2]}`, name: m[1], startAt: endAt - 14 * DAY, endAt, daysLeft, endsLabel,
  };
}

// ── users and spaces (shared across standups) ──
const users = new Map();
for (const s of sample.standups) {
  for (const p of s.people) if (!users.has(p.id)) users.set(p.id, { accountId: p.id, displayName: p.name, initials: p.ini });
}

const spaces = [];
const boards = [];
const configs = [];
const views = [];

sample.standups.forEach((s, si) => {
  const id = si + 1;
  const [boardName, kindText] = s.board.split(" · ");
  const kind = kindText.toLowerCase();
  if (kind !== "scrum" && kind !== "kanban") fail(`unknown board kind ${kindText}`);
  const spaceName = s.space.split(" · ")[1];
  const spaceId = String(10100 + si);
  const boardId = String(10200 + si);
  const scope = kind === "scrum" ? "sprint" : "board";
  const doneWindow = kind === "scrum" ? "sprint" : "14d";
  spaces.push({ id: spaceId, key: s.key, name: spaceName });
  boards.push({ spaceKey: s.key, id: boardId, name: boardName, type: kind, columns: boardColumns });

  // Tickets: sample order is board rank order.
  const built = s.tickets.map((t, ti) => {
    const segs = segmentsOf(t.hist);
    const pulse = buildPulse(t, segs);
    const doneIndex = DAYS - t.days;
    const isDone = t.s === "done";
    return {
      sample: t,
      isDone,
      visible: !isDone || doneWindow === "sprint" || t.days <= 14,
      view: {
        id: id * 100 + ti + 1,
        sourceId: String(10000 + id * 100 + ti),
        key: t.k,
        summary: t.t,
        url: `https://example.atlassian.net/browse/${t.k}`,
        type: t.ty,
        assignee: t.a ?? "",
        lane: t.s,
        statusName: LANE_STATUS[t.s],
        daysInLane: t.days,
        overLimit: !isDone && LIMIT[t.s] > 0 && t.days > LIMIT[t.s],
        blocked: t.bl,
        doneAt: isDone ? dayStartMs(Math.max(0, doneIndex)) : 0,
        pulse,
      },
    };
  });
  const tickets = built.filter((b) => b.visible).map((b) => b.view);
  const active = built.filter((b) => b.visible && !b.isDone);
  const done = built.filter((b) => b.visible && b.isDone);
  if (active.length !== s.tickets.filter((t) => t.s !== "done").length) fail(`${s.key}: active tickets dropped`);

  const speakers = s.people.map((p, pi) => {
    const a = active.filter((b) => b.sample.a === p.id).length;
    const d = done.filter((b) => b.sample.a === p.id).length;
    return {
      view: { id: p.id, name: p.name, first: p.name.split(" ")[0], initials: p.ini, unassigned: false, included: true, active: a, done: d },
      config: { accountId: p.id, name: p.name, initials: p.ini, position: pi, included: true, manual: false, active: a, note: a === 0 ? "no tickets right now" : "" },
    };
  });
  const unassignedActive = active.filter((b) => b.sample.a === null).length;
  const unassignedDone = done.filter((b) => b.sample.a === null).length;
  if (unassignedActive + unassignedDone > 0) {
    speakers.push({
      view: { id: "unassigned", name: "Unassigned", first: "Unassigned", initials: "··", unassigned: true, included: true, active: unassignedActive, done: unassignedDone },
      config: null,
    });
  }

  const backlog = s.backlog.slice(0, BACKLOG_ROWS).map((b, bi) => ({ issueId: id * 10000 + bi + 1, key: b.k, summary: b.t, type: b.ty, url: `https://example.atlassian.net/browse/${b.k}` }));
  const sprint = kind === "scrum" ? sprintOf(s.sprint) : null;
  const previewKey = pickPreview(tickets);

  const sync = { standupId: id, runId: 0, state: "idle", mode: "incremental", phase: "", done: 0, total: 0, lastSuccessAt: NOW, lastAttemptAt: NOW, lastStatus: "ok", warnings: [], errorCode: "", errorMessage: "", needsSync: false };

  views.push({
    standupId: id,
    rev: 3,
    generatedAt: NOW,
    today: TODAY,
    asOf: NOW,
    live: true,
    name: s.name,
    spaceKey: s.key,
    boardName,
    boardType: kind,
    scope,
    sprint,
    pulseDays: DAYS,
    axis,
    lanes: laneInfos(),
    speakers: speakers.map((x) => x.view),
    tickets,
    backlog,
    backlogTotal: s.backlog.length,
    counts: { active: active.length, unassigned: unassignedActive, done: done.length, backlog: backlog.length },
    options: { rowsMode: "fit", showCommits: true, showPRs: true, showComments: true, shadeWeekends: true, doneWindow, doneLabel: s.doneLabel, devActivity: "ok" },
    sync,
    warnings: [],
    previewKey,
    lastSyncedAt: NOW,
  });

  configs.push({
    id,
    rev: 3,
    position: si,
    name: s.name,
    space: { id: spaceId, key: s.key, name: spaceName },
    board: { id: boardId, name: boardName, type: kind },
    scope,
    extraFilter: "",
    filterCheck: { ok: true, errors: [], checkedAt: NOW },
    columns: boardColumns.map((c) => ({ ...c, statusNames: [...c.statusNames] })),
    lanes: WALK.map((lane) => ({ lane, label: LANE_LABEL[lane], limitDays: LIMIT[lane], columns: columnsFor(lane) })),
    unmappedStatuses: [],
    rowsMode: "fit",
    speakers: speakers.filter((x) => x.config).map((x) => x.config),
    unassignedPosition: "last",
    reshuffleDaily: false,
    pulseDays: DAYS,
    showCommits: true,
    showPRs: true,
    showComments: true,
    shadeWeekends: true,
    doneWindow,
    backlogRows: BACKLOG_ROWS,
    needsSync: false,
    draft: false,
  });
});

/** The ticket Configure › Pulse previews: most total marks, blocked preferred, then board rank (architecture §3.2). */
function pickPreview(tickets) {
  let best = null;
  let bestScore = -1;
  for (const t of tickets) {
    const score = t.pulse.marks.reduce((a, m) => a + m.commits + m.prs + m.comments, 0);
    if (score > bestScore || (score === bestScore && t.blocked && !best.blocked)) {
      best = t;
      bestScore = score;
    }
  }
  return best ? best.key : "";
}

// ── app-level fixtures ──
const app = {
  now: NOW,
  build: { version: "0.0.0-mock", commit: "mock", date: TODAY, goVersion: "mock", os: "darwin", arch: "arm64", dev: true },
  settings: { theme: "system", text_size: "standard", sync_on_open: "stale", sync_stale_hours: "6", history_retention_days: "0" },
  connection: {
    state: "ok", source: "jira", baseUrl: "https://example.atlassian.net", account: "mock.user", displayName: "Mock User", devActivity: "ok",
    capabilities: { hasSprints: true, hasBacklogRank: true, hasFlagging: true, hasDevActivity: true, hasExtraFilter: true, hasAssigneeHistory: false, content: true, writes: { transition: true, assign: true, flag: true, comment: true }, filterLanguage: "JQL", authFields: ["baseUrl", "account", "secret"] },
  },
  ui: { lastStandupId: 1, lastView: "standup", lastPerson: { "1": "iris" }, window: null },
  sinkKinds: [
    {
      kind: "file", displayName: "File / JSON",
      capabilities: { current: true, range: false, formats: ["json"], needsSecret: false, idempotent: true },
      schema: [{ key: "folder", label: "Folder", help: "Each send writes one JSON file into this folder.", type: "path", options: null, required: true, default: "" }],
    },
  ],
  diagnostics: {
    build: null, os: "darwin", dataDir: "/mock/data", logDir: "/mock/logs", dbBytes: 0, schemaVersion: 1, recentRuns: [],
  },
};
app.diagnostics.build = app.build;

const catalog = { spaces, boards, users: [...users.values()] };

mkdirSync(outDir, { recursive: true });
const write = (name, value) => writeFileSync(resolve(outDir, name), `${JSON.stringify(value, null, 2)}\n`);
write("config.json", configs);
write("catalog.json", catalog);
write("app.json", app);
sample.standups.forEach((s, si) => write(`view_${s.key.toLowerCase()}.json`, views[si]));
console.log(`mockfixture: wrote ${configs.length} standups to ${outDir}`);
