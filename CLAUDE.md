# CLAUDE.md

**Hand-off to Claude Code.** This repo holds Cuisle's complete design and plan. Nothing is implemented yet; your job is
to build it.

Project guidance for AI agents lives in [AGENTS.md](AGENTS.md). Read it before changing anything.

## Start here
0. Install the build subagents: `mkdir -p .claude/agents && cp scripts/agent/subagents/*.md .claude/agents/`,
   then restart the session or run `/agents` so they load.
1. Read `AGENTS.md` (invariants, repo map, commands, CI and publishing rules).
2. Open [docs/agent-plan.md](docs/agent-plan.md). You are the **orchestrator**:
   - Do Wave 0 yourself: the Wails v3 spike, scaffold, frozen contracts, and failing tests via the
     `cuisle-test-author` subagent.
   - Then fan out Waves 1–4 to `cuisle-implementer` (Haiku) subagents in worktrees, gated by `cuisle-reviewer`.
3. Track progress against [docs/roadmap.md](docs/roadmap.md) M0–M7 and tick the boxes below as milestones close.
4. Stop at the human checkpoints in agent-plan §6 and ask Chris.
5. `git init` is yours. The Forgejo remote is `https://github.com/crueber/cuisle.git`. Push only when Chris
   asks, and never to GitHub.

All open questions from planning are resolved (see the decisions log in AGENTS.md §10). If something genuinely isn't
covered, ask Chris rather than guessing on anything irreversible.

## Progress
Legend: [x] done · [~] implemented, automated checks green, and only a human or CI step remains (in brackets).
- [x] Wave 0: spike, scaffold, contracts, failing tests
- [~] M0 Foundations [Forgejo CI green: blocked on repo secrets REGISTRY_USER/REGISTRY_TOKEN + cuisle-ci pull access]
- [x] M1 Data layer
- [~] M2 Source adapter + fake Jira + Connect [manual Connect against a real site (UAT)]
- [~] M3 Sync + history recording [one board synced against a real site (UAT)]
- [x] M4 View models, pulse, export, sinks (all §6.1 Go benches pass; baselines in bench/)
- [~] M5 Standup screen [UAT visual sign-off]
- [~] M6 Configure incl. Export [UAT: autosave survives restart, visual sign-off]
- [~] M7 Polish, themes, a11y, perf [T §6 manual script in UAT; Windows build after a GitHub publish]
- [ ] M8 Self-signed release (Chris publishes)
- [~] M9 Ticket modal (F12, ADR 0015) [UAT: visual sign-off vs ticket-*.png; real-site writes]

Status (2026-11-23): every WP (01–38) merged. Go suite, 1293 Vitest and 139 Playwright tests (perf, axe both
themes, contrast, 14 screenshots) green; all budgets green (JS 44.8 KB gz; packaged cold start 363–398 ms with 5 standups on
macOS). `task dev`, `task package` work. Development is complete pending CI and UAT.
Remaining follow-ups: docs/wp-notes/orchestrator-backlog.md.

@AGENTS.md
