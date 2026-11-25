package app

// The palette's local reads (F14, ux-spec §15): TicketService.FindByKey (by key and by title), TicketService.ByIDs and
// StandupService.People. LOCAL ONLY: one reader transaction each, never the network (AGENTS invariant 2; a test asserts
// it). The matching is internal/keyfind; the SQL is internal/cache/find.go.

import (
	"context"
	"database/sql"
	"sort"
	"time"

	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/keyfind"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/settings"
	"github.com/crueber/cuisle/internal/view"
)

const (
	findDefaultLimit = 20
	findPrefixShown  = 10
	findCloseShown   = 5
	findTitleShown   = 10
)

// FindByKey is the palette's ticket finder (ux-spec §15.3): LOCAL ONLY, zero network. It matches the key (the tiers
// exact, prefix, close) and the summary (the tier title); the key tiers come first. Title matching is skipped only for a
// query with under two characters; numbers search titles too ("1429" lists its key matches, then titles containing
// "1429"; "404" finds "Fix the 404 page"). A ticket that matches both ways is listed once, in its key tier. currentStandupID (0 = none) puts that standup's tickets first
// within a tier and is the lane mapping of a ticket that is on no board. limit caps the total hits (<= 0 means 20).
func (s *TicketService) FindByKey(ctx context.Context, query string, currentStandupID int64, limit int) (out view.TicketFind, err error) {
	defer s.d.guard("FindByKey", &err)
	out = view.TicketFind{Hits: []view.TicketHit{}}
	kq, keyOK := keyfind.Parse(query)
	tq, titleOK := keyfind.ParseTitle(query)
	if !keyOK && !titleOK {
		return out, nil
	}
	if limit <= 0 {
		limit = findDefaultLimit
	}
	row, ok, err := s.d.connRow(ctx)
	if err != nil || !ok {
		return out, err
	}
	asOf := s.d.viewTime()
	err = s.d.DB.ReadTx(ctx, func(tx *sql.Tx) error {
		type cand struct {
			id      int64
			key     string
			tier    string
			project string
			num     int
			score   int  // title tier: the match score
			cur     bool // a live member of the current standup
			done    bool
		}
		var fc cache.FindContext
		fcLoaded := false
		loadFC := func() error {
			if fcLoaded {
				return nil
			}
			var err error
			fc, err = cache.LoadFindContextTx(ctx, tx, row.ID)
			fcLoaded = err == nil
			return err
		}
		// place sets cur and done from the standups the issue is a member of and its current status.
		place := func(c *cand, status string) {
			members := fc.Members[c.id]
			home := homeStandup(members, currentStandupID)
			c.cur = currentStandupID != 0 && home == currentStandupID && len(members) > 0
			c.done = fc.DoneStatuses[home][status] || fc.DoneCategory[status]
		}
		inOrder := func(a, b *cand) bool {
			switch {
			case a.cur != b.cur:
				return a.cur
			case a.done != b.done:
				return !a.done
			case a.project != b.project:
				return a.project < b.project
			}
			return a.num < b.num
		}

		// Keys.
		var keyCands []cand
		if keyOK {
			keys, err := cache.FindKeysTx(ctx, tx, row.ID, kq.Letters, len(kq.Digits), kq.Digits, func(key []byte) bool {
				_, ok := keyfind.Tier(kq, string(key))
				return ok
			})
			if err != nil {
				return err
			}
			if len(keys) > 0 {
				if err := loadFC(); err != nil {
					return err
				}
				matched := make([]int64, len(keys))
				for i, k := range keys {
					matched[i] = k.ID
				}
				statusOf, err := cache.StatusIDsTx(ctx, tx, matched)
				if err != nil {
					return err
				}
				keyCands = make([]cand, 0, len(keys))
				for _, k := range keys {
					tier, _ := keyfind.Tier(kq, k.Key)
					project, _, _ := keyfind.SplitKey(k.Key)
					c := cand{id: k.ID, key: k.Key, tier: tier, project: project, num: keyfind.Number(k.Key)}
					place(&c, statusOf[k.ID])
					keyCands = append(keyCands, c)
				}
			}
		}

		// Titles.
		var titleCands []cand
		var tm *keyfind.TitleMatcher
		if titleOK {
			if err := loadFC(); err != nil {
				return err
			}
			idx, err := s.d.titleIndexTx(ctx, tx, row.ID)
			if err != nil {
				return err
			}
			tm = keyfind.NewTitleMatcher(tq)
			seen := make(map[int64]bool, len(keyCands))
			for _, c := range keyCands {
				seen[c.id] = true
			}
			for i := range idx.ents {
				e := &idx.ents[i]
				sc, ok := tm.ScoreFolded(idx.arena[e.off : e.off+e.n])
				if !ok || seen[e.id] { // an issue that is a key hit is shown once, in its key tier
					continue
				}
				project, _, _ := keyfind.SplitKey(e.key)
				c := cand{id: e.id, key: e.key, tier: keyfind.Title, project: project, num: keyfind.Number(e.key), score: sc}
				place(&c, e.statusID)
				titleCands = append(titleCands, c)
			}
		}
		if len(keyCands) == 0 && len(titleCands) == 0 {
			return nil
		}

		byTier := map[string][]cand{}
		for _, c := range keyCands {
			byTier[c.tier] = append(byTier[c.tier], c)
		}
		for _, list := range byTier {
			sort.Slice(list, func(i, j int) bool { return inOrder(&list[i], &list[j]) })
		}
		sort.Slice(titleCands, func(i, j int) bool {
			a, b := &titleCands[i], &titleCands[j]
			if a.score != b.score {
				return a.score > b.score
			}
			return inOrder(a, b)
		})
		byTier[keyfind.Title] = titleCands

		var seeds []hitSeed
		room := limit
		take := func(tier string, max int) int {
			list := byTier[tier]
			n := len(list)
			if n > max {
				n = max
			}
			if n > room {
				n = room
			}
			for _, c := range list[:n] {
				var match []int
				if tier != keyfind.Title {
					_, match, _ = keyfind.Classify(kq, c.key)
				}
				seeds = append(seeds, hitSeed{id: c.id, tier: c.tier, match: match})
			}
			room -= n
			return len(list) - n
		}
		take(keyfind.Exact, limit)
		out.PrefixMore = take(keyfind.Prefix, findPrefixShown)
		take(keyfind.Close, findCloseShown)
		out.TitleMore = take(keyfind.Title, findTitleShown)
		hits, err := s.d.buildHits(ctx, tx, row.ID, fc, seeds, currentStandupID, asOf)
		if err != nil {
			return err
		}
		for i := range hits {
			if hits[i].Tier == keyfind.Title {
				// nil when the summary no longer matches (an index a write has just outdated): [] not null, for the frontend
				hits[i].TitleMatch = append([]int{}, tm.Indexes(hits[i].Summary)...)
			}
		}
		out.Hits = hits
		return nil
	})
	if out.Hits == nil {
		out.Hits = []view.TicketHit{}
	}
	return out, err
}

// ByIDs returns one hit per known issue id, in the order given (unknown ids are skipped), for the palette's recent
// tickets. Tier is empty and Match is empty. LOCAL ONLY.
func (s *TicketService) ByIDs(ctx context.Context, issueIDs []int64, currentStandupID int64) (out []view.TicketHit, err error) {
	defer s.d.guard("ByIDs", &err)
	out = []view.TicketHit{}
	if len(issueIDs) == 0 {
		return out, nil
	}
	row, ok, err := s.d.connRow(ctx)
	if err != nil || !ok {
		return out, err
	}
	asOf := s.d.viewTime()
	err = s.d.DB.ReadTx(ctx, func(tx *sql.Tx) error {
		fc, err := cache.LoadFindContextTx(ctx, tx, row.ID)
		if err != nil {
			return err
		}
		seeds := make([]hitSeed, 0, len(issueIDs))
		for _, id := range issueIDs {
			seeds = append(seeds, hitSeed{id: id})
		}
		hits, err := s.d.buildHits(ctx, tx, row.ID, fc, seeds, currentStandupID, asOf)
		if err != nil {
			return err
		}
		out = hits
		return nil
	})
	return out, err
}

// findCfgEntry is a standup's effective config as of rev.
type findCfgEntry struct {
	rev int64
	cfg *model.EffectiveConfig
}

type hitSeed struct {
	id    int64
	tier  string
	match []int // key ink indexes
}

// homeStandup is the standup a ticket opens in: the current one when it is a live member, else the first member by tab
// order, else the current one (0 = none).
func homeStandup(members []int64, current int64) int64 {
	for _, m := range members {
		if m == current {
			return current
		}
	}
	if len(members) > 0 {
		return members[0]
	}
	return current
}

// buildHits is the one enrichment path of FindByKey and ByIDs: facts of the seeds' issues, each placed in its home
// standup's lane mapping. Seeds whose issue is unknown (or on another connection) are skipped; order is kept.
func (d *Deps) buildHits(ctx context.Context, tx *sql.Tx, connID int64, fc cache.FindContext, seeds []hitSeed, current int64, asOf time.Time) ([]view.TicketHit, error) {
	ids := make([]int64, len(seeds))
	for i, s := range seeds {
		ids[i] = s.id
	}
	facts, err := cache.LoadHitFactsTx(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	cfgOf := func(id int64) (*model.EffectiveConfig, error) {
		if id == 0 {
			return nil, nil
		}
		rev, live := fc.Revs[id]
		if live {
			d.findCfgMu.Lock()
			e, ok := d.findCfg[id]
			d.findCfgMu.Unlock()
			if ok && e.rev == rev {
				return e.cfg, nil
			}
		}
		cfg, err := settings.EffectiveTx(ctx, tx, id)
		if err != nil {
			return nil, nil // gone or unreadable: no lane mapping to look from
		}
		if live {
			d.findCfgMu.Lock()
			if d.findCfg == nil {
				d.findCfg = map[int64]findCfgEntry{}
			}
			d.findCfg[id] = findCfgEntry{rev: rev, cfg: &cfg}
			d.findCfgMu.Unlock()
		}
		return &cfg, nil
	}
	out := make([]view.TicketHit, 0, len(seeds))
	for _, s := range seeds {
		f, ok := facts[s.id]
		if !ok {
			continue
		}
		members := fc.Members[s.id]
		home := homeStandup(members, current)
		cfg, err := cfgOf(home)
		if err != nil {
			return nil, err
		}
		h := view.BuildHit(f, cfg, asOf)
		h.StandupID = home
		h.StandupIDs = append([]int64{}, members...)
		h.Removed = len(members) == 0
		h.Tier = s.tier
		if s.match != nil {
			h.Match = s.match
		}
		out = append(out, h)
	}
	return out, nil
}

// People is every speaker of every live standup, deduplicated by account (the palette's `@` mode). Local only.
func (s *StandupService) People(ctx context.Context) (out []view.PersonHit, err error) {
	defer s.d.guard("People", &err)
	out = []view.PersonHit{}
	err = s.d.DB.ReadTx(ctx, func(tx *sql.Tx) error {
		speakers, err := cache.SpeakersTx(ctx, tx)
		if err != nil {
			return err
		}
		active, err := cache.ActiveByAssigneeTx(ctx, tx)
		if err != nil {
			return err
		}
		idx := map[string]int{}
		for _, sp := range speakers {
			i, seen := idx[sp.AccountID]
			if !seen {
				idx[sp.AccountID] = len(out)
				i = len(out)
				out = append(out, view.PersonHit{
					AccountID: sp.AccountID, DisplayName: sp.DisplayName, Initials: view.Initials(sp.DisplayName),
					Standups: []view.PersonStanding{},
				})
			}
			out[i].Standups = append(out[i].Standups, view.PersonStanding{StandupID: sp.StandupID, Active: active[sp.StandupID][sp.AccountID]})
		}
		return nil
	})
	return out, err
}
