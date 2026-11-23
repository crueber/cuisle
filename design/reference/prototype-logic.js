// Cuisle — prototype layout logic (REFERENCE ONLY, not production code).
// Extracted from the design-canvas prototypes. These methods computed every derived value the
// mockups render: the pulse track geometry, the person-view density ladder, the below-the-fold
// summary, board lane balancing, and the Configure screen's state handling.
// Port the *rules* (documented in docs/ux-spec.md) into Go/TS; do not copy this structure.
// `this.props`, `this.state`, `this.setState` come from the prototype runtime.

// ===== shared core (standup selection, people, tickets) =====
const CoreMethods = {
  D() { if (!this._d) { this._d = window.SAMPLE_DATA; } return this._d; }
  core() {
    const d = this.D();
    const st = this.state || {};
    const sbId = st.sb !== undefined ? st.sb : (this.props.standup ?? 'svc');
    const B = d.standups.find((b) => b.id === sbId) || d.standups[0];
    const sel = st.sel !== undefined ? st.sel : (this.props.person ?? 'iris');
    const SL = { todo: 'To Do', prog: 'In Progress', review: 'In Review', qa: 'QA', done: 'Done' };
    const SLA = { todo: 10, prog: 5, review: 2, qa: 3, done: 99 };
    const pm = {};
    B.people.forEach((p) => { pm[p.id] = p; });
    const flags = (t) => ({ isStory: t.ty === 'story', isBug: t.ty === 'bug', isTask: t.ty === 'task', isSpike: t.ty === 'spike' });
    const tix = B.tickets.map((t) => {
      let quiet = 0;
      for (let i = t.ev.length - 1; i >= 0 && t.ev[i] === 0; i--) quiet++;
      return Object.assign({}, t, flags(t), {
        ini: t.a ? pm[t.a].ini : '··', un: !t.a, sl: SL[t.s], sla: SLA[t.s], isDone: t.s === 'done',
        stale: t.s !== 'done' && t.s !== 'todo' && t.days > SLA[t.s], quiet: quiet,
      });
    });
    const bl = B.backlog.map((t) => Object.assign({}, t, flags(t)));
    const ids = B.people.map((p) => p.id);
    const people = B.people.map((p) => {
      const mine = tix.filter((t) => t.a === p.id);
      const cnt = {};
      ['todo', 'prog', 'review', 'qa', 'done'].forEach((s) => { cnt[s] = mine.filter((t) => t.s === s).length; });
      return Object.assign({}, p, {
        first: p.name.split(' ')[0], on: p.id === sel, cnt: cnt,
        n: mine.length - cnt.done, nd: cnt.done,
        pick: () => this.setState({ sel: p.id }),
      });
    });
    const UN = { id: 'none', name: 'Unassigned', ini: '··' };
    const person = sel === 'all' ? null : (sel === 'none' ? UN : (pm[sel] || null));
    const unMine = tix.filter((t) => !t.a);
    const unPick = () => this.setState({ sel: 'none' });
    const next = () => {
      const i = ids.indexOf(sel);
      this.setState({ sel: i < 0 ? ids[0] : (i + 1 < ids.length ? ids[i + 1] : 'all') });
    };
    const boards = d.standups.map((b, i) => ({
      id: b.id, name: b.name, key: b.key, num: String(i + 1), on: b.id === B.id,
      n: b.tickets.filter((t) => t.s !== 'done').length,
      pick: () => this.setState({ sb: b.id, sel: 'all' }),
    }));
    return {
      d, B, boards, sel, SL, SLA, tix, bl, people, person, next,
      pickAll: () => this.setState({ sel: 'all' }),
      isAll: !person, isPerson: !!person,
      mine: person ? (person.id === 'none' ? unMine : tix.filter((t) => t.a === person.id)) : [],
      un: { n: unMine.filter((t) => !t.isDone).length, nd: unMine.filter((t) => t.isDone).length, on: sel === 'none', pick: unPick },
    };
  }
};

// ===== standup screen (person view + whole board) =====
function standupRenderVals() {
    const c = this.core();
    const T = THEME;
    const VW = parseInt(this.props.vw ?? 1920, 10), VH = parseInt(this.props.vh ?? 1080, 10);
    const W = 21;
    const doneMode = 'list', blMode = 'top', blN = 12;
    const pc = (x) => (x / W * 100).toFixed(3) + '%';

    // ---------- layout decisions
    const railFull = VW >= 1360;
    const railW = railFull ? 208 : 76;
    const padX = VW >= 1600 ? 32 : 20;
    const mainW = VW - railW - 2 - padX * 2;
    const showSync = VW >= 1700, showDate = VW >= 1360, cfgFull = VW >= 1280;
    const rightW = 24 + (cfgFull ? 140 : 48) + (showDate ? 140 : 0) + (showSync ? 170 : 0);
    const tabAvail = VW - 28 - 160 - rightW - 56;
    const fullW = (b) => 32 + 26 + 10 + b.name.length * 9.4 + 10 + b.key.length * 8.6;
    const keyW = (b) => 30 + 26 + 8 + b.key.length * 11;
    const sumFull = c.boards.reduce((a, b) => a + fullW(b), 0), sumKey = c.boards.reduce((a, b) => a + keyW(b), 0);
    const tabMode = sumFull <= tabAvail ? 'full' : (sumKey <= tabAvail ? 'key' : 'menu');
    const legendMode = mainW >= 1560 ? 'full' : (mainW >= 1300 ? 'short' : 'button');
    const trackW = mainW >= 1500 ? 600 : (mainW >= 1150 ? 440 : 300);
    const tw = trackW / W;

    const curB = c.boards.find((b) => b.on);
    const tabs = c.boards.map((b) => Object.assign({}, b, {
      ul: b.on ? T.fg : 'transparent', fg: b.on ? T.fg : T.muted, fw: b.on ? 700 : 600, kbd: b.on ? T.fg : T.rule3,
    }));

    // ---------- tracks
    const track = (t, rowH, big) => {
      const total = t.hist.reduce((a, h) => a + h.d, 0);
      let x = W - total;
      const segH = rowH >= 40 ? 12 : (rowH >= 32 ? 10 : 8);
      const segTop = rowH - segH - 6;
      const segs = [];
      t.hist.forEach((h) => {
        const a = Math.max(0, x), b = Math.min(W, x + h.d);
        if (b > a) segs.push({ l: pc(a), w: 'calc(' + pc(b - a) + ' - 2px)', bg: t.isDone ? (h.s === 'done' ? T.tones.done : T.doneTone) : T.tones[h.s], top: segTop, h: segH });
        x += h.d;
      });
      const ticks = [];
      if (big) {
        const ev = t.ev.slice(t.ev.length - W), cm = t.cm.slice(t.cm.length - W), start = Math.max(0, W - total);
        const room = segTop - 3, unit = Math.max(3, Math.floor((room - 6) / 3));
        ev.forEach((v, i) => {
          if (!v || i < start) return;
          const l = 'calc(' + pc(i + 0.5) + ' - ' + (v === 3 && !cm[i] ? 4 : 2) + 'px)';
          if (cm[i]) ticks.push({ l: l, h: 6, w: 4, bg: T.tickC, r: 'none', top: segTop - 8 });
          else if (v === 3) ticks.push({ l: l, h: 8, w: 8, bg: T.tick, r: 'rotate(45deg)', top: segTop - 12 });
          else { const hh = 3 + v * unit; ticks.push({ l: l, h: hh, w: 4, bg: T.tick, r: 'none', top: segTop - 2 - hh }); }
        });
      }
      const bd = Math.min(t.blD, W), hot = !t.isDone && (t.stale || t.bl);
      return {
        segs: segs, ticks: ticks, hatch: t.bl, hl: pc(W - bd), hw: pc(bd), hTop: segTop - 2, hH: segH + 4,
        ic: t.isDone ? T.doneIc : T.ic[t.ty], fg: t.isDone ? T.doneFg : T.fg,
        dBg: hot ? T.alertBg : 'transparent', dFg: hot ? T.alertFg : (t.isDone ? T.doneFg : T.muted),
      };
    };
    const axis = this.D().axis.slice(28 - W).map((a) => Object.assign({}, a, {
      bg: a.we ? T.weekend : 'transparent', fg: a.mon ? T.fg : (a.we ? T.rule3 : T.muted), fw: a.mon ? 700 : 500,
      lab2: tw >= 18 || a.mon ? a.lab : '',
    }));
    const monLabels = this.D().axis.slice(28 - W).map((a, i) => ({ a: a, i: i })).filter((o) => o.a.mon && o.i < W - 1).map((o) => ({ l: pc(o.i), lab: (tw >= 16 ? 'Mon ' : '') + o.a.lab }));

    // ---------- rail
    const people = c.people.map((p) => Object.assign({}, p, {
      bg: p.on ? T.selBg : 'transparent', fg: p.on ? T.selFg : T.railFg, cfg: p.on ? T.selFg : T.railMuted,
    }));
    const un = Object.assign({}, c.un, { bg: c.un.on ? T.selBg : 'transparent', fg: c.un.on ? T.selFg : T.railFg, cfg: c.un.on ? T.selFg : T.railMuted });
    const order = String(this.props.walkOrder ?? 'qa,review,prog,todo').split(',').map((x) => x.trim()).filter((x) => ['qa', 'review', 'prog', 'todo'].indexOf(x) >= 0);
    const maxK = Math.max.apply(null, c.tix.concat(c.bl).map((t) => t.k.length));

    // ---------- person view with vertical fit ladder
    const SIZES = [
      { row: 46, head: 40, tfs: 20, kfs: 15, name: 'roomy rows', gutter: false },
      { row: 38, head: 32, tfs: 18, kfs: 14, name: 'compact rows', gutter: false },
      { row: 34, head: 7, tfs: 17, kfs: 14, name: 'side headings', gutter: true },
    ];
    let pv = { groups: [] };
    let P = SIZES[0];
    if (c.person) {
      const act = order.map((s) => ({ s: s, label: c.SL[s], items: c.mine.filter((t) => t.s === s), done: false })).filter((g) => g.items.length);
      const doneT = c.mine.filter((t) => t.isDone);
      const all = doneT.length ? act.concat([{ s: 'done', label: 'Done', items: doneT, done: true }]) : act;
      const titleH = VH >= 800 ? 56 : 46;
      const H = VH - 64 - (VH >= 800 ? 20 : 12) - titleH - 30 - 10;
      const need = (S) => all.reduce((acc, g) => acc + S.head + g.items.length * S.row, 0);
      P = SIZES.find((S) => need(S) <= H) || SIZES[SIZES.length - 1];
      const fits = need(P) <= H;
      // what sits below the fold (fold leaves room for the scroll bar)
      const fold = H - 36;
      let y = 0;
      const below = [];
      all.forEach((g) => {
        y += P.head;
        let n = 0;
        g.items.forEach(() => { y += P.row; if (y > fold) n++; });
        if (n) below.push(n + ' ' + g.label);
      });
      const groups = all.map((g) => ({
        label: g.label, n: g.items.length, isDone: g.done, notDone: !g.done,
        hbd: g.done ? T.rule3 : T.fg, hfg: g.done ? T.doneFg : T.fg,
        items: g.items.map((t) => Object.assign({}, t, track(t, P.row, !g.done))),
      }));
      pv = {
        name: c.person.name, groups: groups, nd: doneT.length, na: c.mine.length - doneT.length,
        scroll: !fits, belowTxt: below.join(' · '), density: P.name, titleH: titleH,
        notRoomy: P.name !== 'roomy rows', gutter: !!P.gutter, stacked: !P.gutter, empty: c.mine.length === 0,
      };
    }

    // ---------- board view
    const cols = mainW >= 1150 ? 2 : 1;
    const colW = (mainW - 40) / cols;
    const titleW = mainW >= 1500 ? 250 : Math.max(150, Math.min(300, Math.round(colW - (Math.ceil(maxK * 7.9) + 6) - 14 - 36 - 32 - 240)));
    const boardTitleH = VH >= 800 ? 48 : 40;
    const boardAvail = VH - 64 - (VH >= 800 ? 20 : 12) - boardTitleH - 30 - 10;
    const laneOf = (id, name, isUn, rowH) => {
      const rows = [];
      order.forEach((s) => c.tix.filter((t) => (isUn ? !t.a : t.a === id) && t.s === s).forEach((t) => rows.push(Object.assign({}, t, track(t, rowH, false)))));
      const nd = c.tix.filter((t) => (isUn ? !t.a : t.a === id) && t.isDone).length;
      return { name: name, isUn: isUn, notUn: !isUn, rows: rows, n: rows.length, nd: nd, h: 30 + rows.length * rowH + 8, rowH: rowH };
    };
    const buildCols = (rowH) => {
      const lanes = c.people.map((p) => Object.assign(laneOf(p.id, p.name, false, rowH), { pick: people.find((q) => q.id === p.id).pick }));
      const ul = Object.assign(laneOf(null, 'Unassigned', true, rowH), { pick: c.un.pick });
      const cs = [];
      for (let i = 0; i < cols; i++) cs.push({ lanes: [], h: 0 });
      if (ul.n) { cs[0].lanes.push(ul); cs[0].h += ul.h; }
      lanes.slice().sort((a, b) => b.n - a.n).forEach((l) => {
        const tgt = cs.reduce((m, x) => (x.h < m.h ? x : m), cs[0]);
        tgt.lanes.push(l); tgt.h += l.h;
      });
      return { cs: cs, h: Math.max.apply(null, cs.map((x) => x.h)), ul: ul };
    };
    let laneRowH = 23, built = buildCols(23);
    if (built.h > boardAvail) { laneRowH = 20; built = buildCols(20); }
    const doneAll = c.tix.filter((t) => t.isDone).map((t) => Object.assign({}, t, { ic: T.doneIc }));
    const blAll = c.bl.map((t) => Object.assign({}, t, { ic: T.ic[t.ty] }));
    const bottomW = mainW - 40;
    const sideBySide = bottomW >= 1100;
    const dCols = bottomW >= 1300 ? 2 : 1, bCols = bottomW >= 1300 ? 3 : (bottomW >= 1000 ? 2 : 1);
    const blShown = blAll.slice(0, blN);
    const doneRowsN = Math.ceil(doneAll.length / dCols), blRowsN = Math.ceil(blShown.length / bCols);
    const listsH = 4 + 40 + (sideBySide ? Math.max(doneRowsN, blRowsN) * 26 : (doneRowsN * 26 + 50 + blRowsN * 26));
    // below the fold
    const fold = boardAvail - 36;
    const belowLanes = [];
    built.cs.forEach((col) => { let hh = 0; col.lanes.forEach((l) => { hh += l.h; if (hh > fold) belowLanes.push(l.isUn ? 'Unassigned' : l.name.split(' ')[0]); }); });
    const below = belowLanes.slice();
    if (built.h + 44 > fold) below.push('Done (' + doneAll.length + ')');
    if (built.h + 44 > fold || (!sideBySide && built.h + 44 + doneRowsN * 26 + 50 > fold)) below.push('Backlog (' + blShown.length + ')');
    const boardScroll = built.h + listsH > boardAvail;
    const nActive = c.tix.filter((t) => !t.isDone).length;

    const atEnd = !!(this.state && this.state.atEnd);
    const onScroll = (e) => {
      const el = e.currentTarget || e.target;
      const end = el.scrollTop + el.clientHeight >= el.scrollHeight - 6;
      if (end !== atEnd) this.setState({ atEnd: end });
    };
    return {
      onScroll: onScroll, barLabel: atEnd ? 'End of list' : 'Scroll for more',
      pBarSub: atEnd ? 'every ticket is shown above' : (pv.belowTxt || '') + ' below',
      bBarSub: atEnd ? 'every ticket is shown above' : below.join(' · '),
      L: {
        vw: VW, vh: VH, railW: railW, padX: padX, padTop: VH >= 800 ? 20 : 12, trackW: trackW,
        keyW: Math.ceil(maxK * 7.9) + 6, keyWL: Math.ceil(maxK * (P.kfs * 0.6)) + 6, laneLeft: Math.ceil(maxK * 7.9) + 6 + 30 + titleW,
        titleW: titleW, laneRowH: laneRowH, h1: VH >= 800 ? 36 : 28, h1b: VH >= 800 ? 30 : 24, boardTitleH: boardTitleH,
        row: P.row, head: P.head, tfs: P.tfs, kfs: P.kfs, colsCss: 'repeat(' + cols + ',minmax(0,1fr))',
        segTop: P.row - 18, gGap: P.gutter ? P.head - 3 : 0,
      },
      railFull: railFull, railSmall: !railFull, showSync: showSync, showDate: showDate, cfgFull: cfgFull, cfgIcon: !cfgFull,
      tabsFull: tabMode === 'full', tabsKey: tabMode === 'key', tabsMenu: tabMode === 'menu', tabs: tabs, curB: curB, nBoards: c.boards.length,
      legendFull: legendMode === 'full', legendShort: legendMode === 'short', legendBtn: legendMode === 'button',
      people: people, un: un, pickAll: c.pickAll, isAll: c.isAll, isPerson: c.isPerson,
      allBg: c.isAll ? T.selBg : 'transparent', allFg: c.isAll ? T.selFg : T.railFg,
      axis: axis, monLabels: monLabels, pv: pv,
      laneCols: built.cs, laneCompact: laneRowH < 23,
      boardScroll: boardScroll, boardBelow: below.join(' · '),
      bottomCols: sideBySide ? 'minmax(0,1fr) minmax(0,1.5fr)' : 'minmax(0,1fr)', bottomGap: sideBySide ? 0 : 24,
      doneAll: doneAll, nDone: doneAll.length,
      blShown: blShown, blCount: blShown.length, nBacklog: blAll.length, blMore: blAll.length - blShown.length, hasBlMore: blAll.length > blShown.length,
      doneGrid: 'repeat(' + dCols + ', minmax(0,1fr))', blGrid: 'repeat(' + bCols + ', minmax(0,1fr))',
      doneRows: 'repeat(' + Math.max(1, doneRowsN) + ', 26px)', blRows: 'repeat(' + Math.max(1, blRowsN) + ', 26px)',
      sprint: c.B.sprint, doneLabel: c.B.doneLabel, nActive: nActive, nUn: built.ul.n,
    };
}

// ===== configure screen =====
function configureRenderVals() {
    const d = this.D();
    const T = THEME;
    const VW = parseInt(this.props.vw ?? 1920, 10), VH = parseInt(this.props.vh ?? 1080, 10);
    const railW = VW >= 1360 ? 280 : 230;
    const padX = VW >= 1600 ? 36 : 20;
    const mainW = VW - railW - 2 - padX * 2;
    const ncols = mainW >= 1500 ? 3 : (mainW >= 960 ? 2 : 1);
    const EXTRA = {
      svc: [{ id: 'dana', name: 'Dana Whitfield', ini: 'DW', note: 'Product' }, { id: 'alex', name: 'Alex Moreau', ini: 'AM', note: 'Design' }],
      case: [{ id: 'jordan', name: 'Jordan Pike', ini: 'JP', note: 'Support' }],
      dia: [{ id: 'sam2', name: 'Amara Osei', ini: 'SO', note: 'also on MOB' }],
    };
    const S0 = this.state || {};
    const startNew = !!(this.props.newDraft ?? false) && S0.boards === undefined;
    const baseBoards = d.standups.map((b) => ({ id: b.id, name: b.name, key: b.key, space: b.space, board: b.board, isNew: false }));
    const boards = S0.boards || (startNew ? baseBoards.concat([{ id: 'new1', name: 'New standup', key: '', space: '', board: '', isNew: true }]) : baseBoards);
    const cur = S0.cur || (startNew ? 'new1' : (this.props.standup ?? 'svc'));
    const defCfg = (id) => {
      const src = d.standups.find((b) => b.id === id);
      const ppl = src ? src.people.map((p) => p.id).concat((EXTRA[id] || []).map((p) => p.id)) : [];
      const inc = {};
      ppl.forEach((pid) => { inc[pid] = !(EXTRA[id] || []).some((e) => e.id === pid); });
      return {
        order: ppl, inc: inc, reshuffle: false, unPos: 'last', scope: id === 'dia' ? 'board' : 'sprint', days: 21,
        mCommit: true, mPr: true, mComment: true, limits: { todo: 10, prog: 5, review: 2, qa: 3 }, weekends: 'shade',
        doneMode: 'list', doneWin: id === 'dia' ? '14' : 'sprint', blMode: 'top', blN: 12, walk: 'qa-first', pDone: 'condense',
        seed: 'defaults', note: '', walk: ['qa', 'review', 'prog', 'todo'], rows: 'auto',
      };
    };
    const cfgs = S0.cfgs || {};
    const st = Object.assign(defCfg(cur), cfgs[cur] || {});
    const app = Object.assign({ theme: 'light', size: 'standard', refresh: '2' }, S0.app || {});
    const save = (patch) => this.setState({ boards: boards, cur: cur, app: app, cfgs: Object.assign({}, cfgs, { [cur]: Object.assign({}, st, patch) }) });
    const setApp = (patch) => this.setState({ boards: boards, cur: cur, cfgs: cfgs, app: Object.assign({}, app, patch) });
    const opt = (val, list, apply) => list.map((pair, i) => {
      const on = val === pair[0];
      return { lab: pair[1], on: on, bg: on ? T.btnBg : T.fieldBg, fg: on ? T.btnFg : T.fg, bl: i ? '2px solid ' + T.rule3 : '0', pick: () => apply(pair[0]) };
    });
    const opts = (key, list) => opt(st[key], list, (v) => save({ [key]: v }));
    const appOpts = (key, list) => opt(app[key], list, (v) => setApp({ [key]: v }));
    const sw = (key) => ({ on: st[key], toggle: () => save({ [key]: !st[key] }),
      bg: st[key] ? T.btnBg : T.rule3, x: st[key] ? '22px' : '2px', knob: st[key] ? T.btnFg : T.fieldBg });
    const step = (val, min, max, apply) => ({ v: val, dec: () => apply(Math.max(min, val - 1)), inc: () => apply(Math.min(max, val + 1)) });

    const B = boards.find((b) => b.id === cur) || boards[0];
    const src = d.standups.find((b) => b.id === cur);
    const isNew = !!B.isNew;

    // standups rail
    const rail = boards.map((b, i) => {
      const on = b.id === cur;
      const sd = d.standups.find((x) => x.id === b.id);
      return {
        name: b.name, key: b.key || 'not set', num: String(i + 1), on: on,
        sub: sd ? sd.people.length + ' people' : 'draft',
        bg: on ? T.selBg : 'transparent', fg: on ? T.selFg : T.fg, sfg: on ? T.selFg : T.muted,
        kbd: on ? T.selFg : T.rule3,
        pick: () => this.setState({ boards: boards, cfgs: cfgs, app: app, cur: b.id }),
        up: () => { if (!i) return; const o = boards.slice(); o[i] = o[i - 1]; o[i - 1] = b; this.setState({ boards: o, cfgs: cfgs, app: app, cur: cur }); },
        down: () => { if (i === boards.length - 1) return; const o = boards.slice(); o[i] = o[i + 1]; o[i + 1] = b; this.setState({ boards: o, cfgs: cfgs, app: app, cur: cur }); },
      };
    });
    const addNew = () => {
      const id = 'new' + (boards.length + 1);
      this.setState({ boards: boards.concat([{ id: id, name: 'New standup', key: '', space: '', board: '', isNew: true }]), cfgs: cfgs, app: app, cur: id });
    };
    const dup = () => {
      if (isNew) return;
      const id = cur + '-copy';
      if (boards.some((b) => b.id === id)) return;
      const nb = Object.assign({}, B, { id: id, name: B.name + ' (copy)', isNew: true, copyOf: B.name });
      this.setState({ boards: boards.concat([nb]), cfgs: Object.assign({}, cfgs, { [id]: Object.assign({}, st) }), app: app, cur: id });
    };
    const idx = boards.findIndex((b) => b.id === cur);

    // speakers
    const ppl = {};
    if (src) src.people.forEach((p) => { ppl[p.id] = { name: p.name, ini: p.ini, n: src.tickets.filter((t) => t.a === p.id && t.s !== 'done').length, note: '' }; });
    (EXTRA[cur] || []).forEach((p) => { ppl[p.id] = { name: p.name, ini: p.ini, n: 0, note: p.note }; });
    let num = 0;
    const speakers = st.order.filter((id) => ppl[id]).map((id, i, arr) => {
      const p = ppl[id], on = !!st.inc[id];
      if (on) num++;
      const move = (dd) => { const o = st.order.slice(); const j = i + dd; if (j < 0 || j >= o.length) return; o[i] = o[j]; o[j] = id; save({ order: o, note: '' }); };
      return {
        name: p.name, ini: p.ini, on: on, num: on ? String(num) : '—',
        sub: p.n ? p.n + ' active' : (p.note ? p.note + ' · 0 tickets' : '0 tickets'),
        fg: on ? T.fg : T.muted, boxBg: on ? T.btnBg : T.fieldBg, boxBd: on ? T.btnBg : T.rule3,
        up: () => move(-1), down: () => move(1), toggle: () => save({ inc: Object.assign({}, st.inc, { [id]: !on }) }),
      };
    });
    const reorder = (ids, note) => { const ins = ids.filter((id) => st.inc[id]), outs = st.order.filter((id) => !st.inc[id]); save({ order: ins.concat(outs), note: note }); };
    const shuffle = () => {
      const a = st.order.filter((id) => st.inc[id]);
      for (let i = a.length - 1; i > 0; i--) { const j = Math.floor(Math.random() * (i + 1)); const t = a[i]; a[i] = a[j]; a[j] = t; }
      reorder(a, 'Shuffled just now');
    };
    const az = () => reorder(st.order.slice().sort((x, y) => ppl[x].name.localeCompare(ppl[y].name)), 'Sorted A–Z');
    const load = () => reorder(st.order.slice().sort((x, y) => ppl[y].n - ppl[x].n), 'Most active first');

    // pulse preview
    const W = st.days;
    const pc = (x) => (x / W * 100).toFixed(3) + '%';
    const pool = (src || d.standups[0]).tickets;
    const sample = pool.find((t) => t.bl) || pool.find((t) => t.s === 'prog') || pool[0];
    const total = sample.hist.reduce((a, h) => a + h.d, 0);
    let x = W - total;
    const segs = [];
    sample.hist.forEach((h) => { const a = Math.max(0, x), b = Math.min(W, x + h.d); if (b > a) segs.push({ l: pc(a), w: 'calc(' + pc(b - a) + ' - 2px)', bg: T.tones[h.s] }); x += h.d; });
    const ticks = [];
    const ev = sample.ev.slice(28 - W), cm = sample.cm.slice(28 - W), start = Math.max(0, W - total);
    ev.forEach((v, i) => {
      if (!v || i < start) return;
      const l = 'calc(' + pc(i + 0.5) + ' - ' + (v === 3 && !cm[i] ? 5 : 2) + 'px)';
      if (cm[i]) { if (st.mComment) ticks.push({ l: l, h: 7, w: 4, bg: T.tickC, r: 'none', top: 19 }); }
      else if (v === 3) { if (st.mPr) ticks.push({ l: l, h: 9, w: 9, bg: T.tick, r: 'rotate(45deg)', top: 13 }); }
      else if (st.mCommit) ticks.push({ l: l, h: 6 + v * 5, w: 4, bg: T.tick, r: 'none', top: 26 - (6 + v * 5) });
    });
    const axis = d.axis.slice(28 - W).map((a) => ({ lab: a.lab, bg: a.we && st.weekends === 'shade' ? T.weekend : 'transparent', fg: a.mon ? T.fg : T.muted, fw: a.mon ? 700 : 500 }));
    const lim = (k) => step(st.limits[k], 1, 30, (v) => save({ limits: Object.assign({}, st.limits, { [k]: v }) }));
    const limits = [['qa', 'QA'], ['review', 'In Review'], ['prog', 'In Progress'], ['todo', 'To Do']].map((p) => Object.assign(lim(p[0]), { lab: p[1], tone: T.tones[p[0]] }));
    const blD = sample.blD ? Math.min(W, sample.blD) : 0;

    const mapping = (cur === 'dia'
      ? [['Ready', 'To Do', T.tones.todo], ['Doing', 'In Progress', T.tones.prog], ['Review', 'In Review', T.tones.review], ['Verify', 'QA', T.tones.qa], ['Done', 'Done', T.tones.done]]
      : [['Backlog', 'Backlog', T.rule3], ['Selected for Development', 'To Do', T.tones.todo], ['In Progress', 'In Progress', T.tones.prog],
         ['Code Review', 'In Review', T.tones.review], ['QA', 'QA', T.tones.qa], ['Ready for Release', 'QA', T.tones.qa], ['Done', 'Done', T.tones.done]]
    ).map((m) => ({ col: m[0], lane: m[1], tone: m[2] }));

    const LANE = { qa: 'QA', review: 'In Review', prog: 'In Progress', todo: 'To Do' };
    const JIRA = cur === 'dia'
      ? { qa: 'Verify', review: 'Review', prog: 'Doing', todo: 'Ready' }
      : { qa: 'QA, Ready for Release', review: 'Code Review', prog: 'In Progress', todo: 'Selected for Development' };
    const walk = st.walk.map((k, i) => {
      const mv = (dd) => { const o = st.walk.slice(); const j = i + dd; if (j < 0 || j >= o.length) return; o[i] = o[j]; o[j] = k; save({ walk: o }); };
      return { num: String(i + 1), lab: LANE[k], jira: 'Jira: ' + JIRA[k], tone: T.tones[k], up: () => mv(-1), down: () => mv(1),
        upOp: i === 0 ? 0.3 : 1, dnOp: i === st.walk.length - 1 ? 0.3 : 1 };
    });
    const isRTL = st.walk.join() === 'qa,review,prog,todo', isLTR = st.walk.join() === 'todo,prog,review,qa';
    const walkNote = isRTL ? 'Right to left (QA first)' : (isLTR ? 'Left to right (To Do first)' : 'Custom order');
    const S = (k) => ({ [k]: true });
    const layout = ncols === 3
      ? [['source', 'person'], ['speakers', 'display'], ['pulse', 'board', 'mapping']]
      : ncols === 2
        ? [['source', 'speakers', 'board'], ['person', 'pulse', 'mapping', 'display']]
        : [['source', 'speakers', 'person', 'pulse', 'board', 'mapping', 'display']];
    return {
      columns: layout.map((c) => ({ secs: c.map(S) })), colsCss: 'repeat(' + ncols + ', minmax(0,1fr))',
      railW: railW, padX: padX, showJump: ncols < 3, vw: VW, vh: VH, showPath: VW >= 1600, saveShort: VW < 1280,
      walk: walk, walkNote: walkNote,
      rtl: () => save({ walk: ['qa', 'review', 'prog', 'todo'] }), ltr: () => save({ walk: ['todo', 'prog', 'review', 'qa'] }),
      rowsOpts: opts('rows', [['auto', 'Fit when possible'], ['roomy', 'Always roomy']]),
      rail: rail, addNew: addNew, dup: dup, nBoards: boards.length, curNum: String(idx + 1),
      curName: B.name, isNew: isNew, notNew: !isNew, copyOf: B.copyOf || '', isCopy: !!B.copyOf,
      saveLabel: isNew ? 'Create standup' : (VW < 1280 ? 'Save' : 'Save & back to standup'),
      spaceText: B.space || 'Choose a space…', boardText: B.board || 'Pick a space first',
      spaceBd: B.space ? T.rule3 : T.fg, boardFg: B.board ? T.fg : T.muted,
      seedOpts: opts('seed', [['defaults', 'Defaults'], ['svc', 'Copy Saffron Mobile']]),
      scopeOpts: opts('scope', [['sprint', 'Active sprint'], ['board', 'Whole board']]),
      speakers: speakers, hasSpeakers: speakers.length > 0, noSpeakers: speakers.length === 0,
      speakSub: speakers.length ? speakers.filter((s) => s.on).length + ' of ' + speakers.length + ' in this standup' : 'listed once a board is picked', shuffle: shuffle, az: az, load: load,
      note: st.note, hasNote: !!st.note, reshuffle: sw('reshuffle'),
      unOpts: opts('unPos', [['first', 'First'], ['last', 'Last'], ['off', 'Skip']]),
      dayOpts: opts('days', [[7, '7'], [14, '14'], [21, '21'], [28, '28']]), nDays: W,
      axis: axis, segs: segs, ticks: ticks, hatch: !!sample.bl, hl: pc(W - blD), hw: pc(blD), sampleKey: sample.k,
      mCommit: sw('mCommit'), mPr: sw('mPr'), mComment: sw('mComment'),
      limits: limits, wkOpts: opts('weekends', [['shade', 'Shade'], ['plain', 'Plain']]),
      doneOpts: opts('doneMode', [['list', 'List'], ['count', 'Count only'], ['hidden', 'Hide']]),
      winOpts: opts('doneWin', [['sprint', 'Sprint'], ['14', '14 days'], ['1', 'Since yesterday']]),
      blOpts: opts('blMode', [['top', 'Top N'], ['count', 'Count only'], ['hidden', 'Hide']]),
      blN: step(st.blN, 0, 40, (v) => save({ blN: v })), blTop: true,
      walkOpts: opts('walk', [['qa-first', 'QA → To Do'], ['todo-first', 'To Do → QA']]),
      pDoneOpts: opts('pDone', [['condense', 'Condense'], ['chips', 'Key chips'], ['hidden', 'Hide']]),
      themeOpts: appOpts('theme', [['light', 'Light'], ['dark', 'Dark'], ['system', 'System']]),
      sizeOpts: appOpts('size', [['standard', 'Standard'], ['large', 'Large (TV)']]),
      refreshOpts: appOpts('refresh', [['1', '1 min'], ['2', '2 min'], ['5', '5 min'], ['manual', 'Manual']]),
      mapping: mapping,
    };
}
