package app

// The finder's title index (F14, ux-spec §15.3): every issue of the connection with its summary already case-folded,
// held in memory so a title query scans memory instead of 5 000 database rows (database/sql alone costs more than the
// finder's 5 ms budget). It is rebuilt when the issue generation (migration 0008, bumped by triggers when an issue is
// added or its key, summary or status changes) differs from the one it was built at. The generation is read in the same
// read transaction as the rows, so the two always agree; settings saves, recents, runs and the like never move it.

import (
	"context"
	"database/sql"

	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/keyfind"
)

type titleEnt struct {
	id       int64
	key      string
	statusID string
	off, n   int32 // the folded summary: arena[off : off+n]
}

type titleIndex struct {
	gen   int64
	conn  int64
	ents  []titleEnt
	arena []rune
}

// titleIndexTx returns the index for the connection, building it in tx when there is none or an issue has changed since.
func (d *Deps) titleIndexTx(ctx context.Context, tx *sql.Tx, connID int64) (*titleIndex, error) {
	gen, err := cache.IssueGenTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	d.findIdxMu.Lock()
	idx := d.findIdx
	d.findIdxMu.Unlock()
	if idx != nil && idx.conn == connID && idx.gen == gen {
		return idx, nil
	}
	d.findIdxBuilds.Add(1)
	idx = &titleIndex{gen: gen, conn: connID}
	statuses := map[string]string{} // status ids repeat across thousands of issues: one string each
	err = cache.LoadTitleEntriesTx(ctx, tx, connID, func(id int64, key, status, summary []byte) {
		st, ok := statuses[string(status)]
		if !ok {
			st = string(status)
			statuses[st] = st
		}
		off := len(idx.arena)
		idx.arena = keyfind.FoldBytes(idx.arena, summary)
		idx.ents = append(idx.ents, titleEnt{id: id, key: string(key), statusID: st, off: int32(off), n: int32(len(idx.arena) - off)})
	})
	if err != nil {
		return nil, err
	}
	d.findIdxMu.Lock()
	d.findIdx = idx
	d.findIdxMu.Unlock()
	return idx, nil
}
