package app

import (
	"context"
	"database/sql"

	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/model"
)

// boardPeople are the assignees the standup's board already knows (local data only).
func (d *Deps) boardPeople(ctx context.Context, row cache.ConnRow, standupID int64) ([]model.Person, error) {
	var people []model.Person
	err := d.DB.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		people, err = cache.BoardAssigneesTx(ctx, tx, row.ID, standupID)
		return err
	})
	return people, err
}
