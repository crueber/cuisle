package app

import (
	"context"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/model"
	syncpkg "github.com/crueber/cuisle/internal/sync"
	"github.com/crueber/cuisle/internal/view"
)

type ConfigService struct{ d *Deps }

func (s *ConfigService) Get(ctx context.Context, standupID int64) (out view.StandupConfig, err error) {
	defer s.d.guard("Get", &err)
	return s.d.standupConfig(ctx, standupID)
}

func (s *ConfigService) Create(ctx context.Context) (out view.StandupConfig, err error) {
	defer s.d.guard("Create", &err)
	id, err := s.d.Settings.Create(ctx)
	if err != nil {
		return view.StandupConfig{}, err
	}
	return s.d.standupConfig(ctx, id)
}

func (s *ConfigService) Duplicate(ctx context.Context, standupID int64) (out view.StandupConfig, err error) {
	defer s.d.guard("Duplicate", &err)
	id, err := s.d.Settings.Duplicate(ctx, standupID)
	if err != nil {
		return view.StandupConfig{}, err
	}
	return s.d.standupConfig(ctx, id)
}

func (s *ConfigService) CopySettingsFrom(ctx context.Context, standupID int64, fromStandupID int64) (out view.StandupConfig, err error) {
	defer s.d.guard("CopySettingsFrom", &err)
	if _, err := s.d.Settings.CopySettingsFrom(ctx, standupID, fromStandupID); err != nil {
		return view.StandupConfig{}, err
	}
	return s.d.standupConfig(ctx, standupID)
}

func (s *ConfigService) DiscardDraft(ctx context.Context, standupID int64) (err error) {
	defer s.d.guard("DiscardDraft", &err)
	return s.d.Settings.DiscardDraft(ctx, standupID)
}

func (s *ConfigService) Delete(ctx context.Context, standupID int64) (ack SaveAck, err error) {
	defer s.d.guard("Delete", &err)
	a, err := s.d.Settings.Archive(ctx, standupID)
	if err != nil {
		return SaveAck{}, err
	}
	return saveAck(a), nil
}

func (s *ConfigService) Restore(ctx context.Context, standupID int64) (ack SaveAck, err error) {
	defer s.d.guard("Restore", &err)
	a, err := s.d.Settings.Restore(ctx, standupID)
	if err != nil {
		return SaveAck{}, err
	}
	// Other windows and the tab strip did not cause this call: tell them (architecture.md section 3.3).
	s.d.emit(syncpkg.EvDataChanged, syncpkg.DataChanged{StandupID: standupID, What: "standups"})
	return saveAck(a), nil
}

func (s *ConfigService) Move(ctx context.Context, standupID int64, toPosition int) (ack SaveAck, err error) {
	defer s.d.guard("Move", &err)
	a, err := s.d.Settings.Move(ctx, standupID, toPosition)
	if err != nil {
		return SaveAck{}, err
	}
	return saveAck(a), nil
}

func (s *ConfigService) Update(ctx context.Context, standupID int64, patch StandupPatch) (ack SaveAck, err error) {
	defer s.d.guard("Update", &err)
	a, err := s.d.Settings.Update(ctx, standupID, patch)
	if err != nil {
		return SaveAck{}, err
	}
	return saveAck(a), nil
}

func (s *ConfigService) SetSpace(ctx context.Context, standupID int64, spaceID string) (out view.StandupConfig, err error) {
	defer s.d.guard("SetSpace", &err)
	// SetSpace resolves the space from the local autocomplete cache (filled by SearchSpaces): no network.
	d := s.d
	row, ok, err := d.connRow(ctx)
	if err != nil {
		return view.StandupConfig{}, err
	}
	if !ok {
		return view.StandupConfig{}, apperr.New(apperr.AuthMissing, "Connect to Jira to choose a space.")
	}
	spaces, err := cache.SearchSpaces(ctx, d.DB.R, row.ID, "", 0)
	if err != nil {
		return view.StandupConfig{}, err
	}
	var space *model.Space
	for i := range spaces {
		if spaces[i].ID == spaceID {
			space = &spaces[i]
			break
		}
	}
	if space == nil {
		return view.StandupConfig{}, apperr.New(apperr.NotFound, "That space is not known yet. Search for it again.")
	}
	if _, err := d.Settings.SetSpace(ctx, standupID, *space); err != nil {
		return view.StandupConfig{}, err
	}
	return d.standupConfig(ctx, standupID)
}

func (s *ConfigService) SetBoard(ctx context.Context, standupID int64, boardID string) (out view.StandupConfig, err error) {
	defer s.d.guard("SetBoard", &err)
	// SetBoard fetches the board's columns and statuses from the source (a board pick is an allowed network trigger),
	// then stores them and auto-maps the columns. A failure leaves the standup untouched.
	d := s.d
	st, err := d.loadLive(ctx, standupID)
	if err != nil {
		return view.StandupConfig{}, err
	}
	if st.SpaceID == "" {
		return view.StandupConfig{}, apperr.Validationf("boardId", "Choose a space before choosing a board.")
	}
	if boardID == "" {
		return view.StandupConfig{}, apperr.Validationf("boardId", "Choose a board.")
	}
	row, ok, err := d.connRow(ctx)
	if err != nil {
		return view.StandupConfig{}, err
	}
	if !ok {
		return view.StandupConfig{}, apperr.New(apperr.AuthMissing, "Connect to Jira to choose a board.")
	}
	if row.AuthState == "invalid" {
		return view.StandupConfig{}, apperr.New(apperr.AuthInvalid, "Jira rejected the token. Reconnect to choose a board.")
	}
	src, err := d.openSource(row, false)
	if err != nil {
		return view.StandupConfig{}, err
	}
	cfg, err := src.BoardConfig(ctx, boardID)
	if err != nil {
		d.noteSourceError(ctx, row, err)
		return view.StandupConfig{}, apperr.From(err)
	}
	statuses, _, err := src.Reference(ctx)
	if err != nil {
		d.noteSourceError(ctx, row, err)
		return view.StandupConfig{}, apperr.From(err)
	}
	// The adapter does not know the space; keep the cached board filed under the standup's space.
	cfg.Board.SpaceID = st.SpaceID
	if _, err := d.Settings.SetBoard(ctx, standupID, cfg, statuses); err != nil {
		return view.StandupConfig{}, err
	}
	// Reference() may have discovered field ids; keep them so the next sync does not rediscover them.
	d.saveSourceMeta(ctx, row, src.Meta())
	return d.standupConfig(ctx, standupID)
}

func (s *ConfigService) SetColumnLane(ctx context.Context, standupID int64, columnName string, lane string) (ack SaveAck, err error) {
	defer s.d.guard("SetColumnLane", &err)
	cfg, err := s.d.Settings.Effective(ctx, standupID)
	if err != nil {
		return SaveAck{}, err
	}
	found := false
	for _, c := range cfg.Columns {
		if c.ColumnName == columnName {
			found = true
			break
		}
	}
	if !found {
		return SaveAck{}, apperr.New(apperr.NotFound, "That column is not on this board.")
	}
	a, err := s.d.Settings.SetColumnLane(ctx, standupID, columnName, lane)
	if err != nil {
		return SaveAck{}, err
	}
	return saveAck(a), nil
}

// MoveColumn reorders a board column among the columns mapped to the same lane; tickets in that lane follow the order.
func (s *ConfigService) MoveColumn(ctx context.Context, standupID int64, columnName string, toIndex int) (ack SaveAck, err error) {
	defer s.d.guard("MoveColumn", &err)
	a, err := s.d.Settings.MoveColumn(ctx, standupID, columnName, toIndex)
	if err != nil {
		return SaveAck{}, err
	}
	return saveAck(a), nil
}

func (s *ConfigService) SetWalkOrder(ctx context.Context, standupID int64, lanes []string) (ack SaveAck, err error) {
	defer s.d.guard("SetWalkOrder", &err)
	a, err := s.d.Settings.SetWalkOrder(ctx, standupID, lanes)
	if err != nil {
		return SaveAck{}, err
	}
	return saveAck(a), nil
}

func (s *ConfigService) SetLaneLimit(ctx context.Context, standupID int64, lane string, days int) (ack SaveAck, err error) {
	defer s.d.guard("SetLaneLimit", &err)
	a, err := s.d.Settings.SetLaneLimit(ctx, standupID, lane, days)
	if err != nil {
		return SaveAck{}, err
	}
	return saveAck(a), nil
}

func (s *ConfigService) SetSpeakerIncluded(ctx context.Context, standupID int64, accountID string, included bool) (ack SaveAck, err error) {
	defer s.d.guard("SetSpeakerIncluded", &err)
	a, err := s.d.Settings.SetSpeakerIncluded(ctx, standupID, accountID, included)
	if err != nil {
		return SaveAck{}, err
	}
	return saveAck(a), nil
}

func (s *ConfigService) MoveSpeaker(ctx context.Context, standupID int64, accountID string, toPosition int) (ack SaveAck, err error) {
	defer s.d.guard("MoveSpeaker", &err)
	a, err := s.d.Settings.MoveSpeaker(ctx, standupID, accountID, toPosition)
	if err != nil {
		return SaveAck{}, err
	}
	return saveAck(a), nil
}

func (s *ConfigService) SortSpeakers(ctx context.Context, standupID int64, mode string) (out []view.SpeakerConfig, err error) {
	defer s.d.guard("SortSpeakers", &err)
	var active map[string]int
	if mode == "active" {
		if active, err = s.d.activeByAccount(ctx, standupID); err != nil {
			return nil, err
		}
	}
	if _, _, err := s.d.Settings.SortSpeakers(ctx, standupID, mode, active, nil); err != nil {
		return nil, err
	}
	return s.speakers(ctx, standupID)
}

func (s *ConfigService) AddSpeaker(ctx context.Context, standupID int64, accountID string) (out []view.SpeakerConfig, err error) {
	defer s.d.guard("AddSpeaker", &err)
	// AddSpeaker adds someone from the local person cache (filled by SearchUsers): no network.
	d := s.d
	if _, err := d.loadLive(ctx, standupID); err != nil {
		return nil, err
	}
	row, ok, err := d.connRow(ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apperr.New(apperr.NotFound, "That person is not known yet. Search for them first.")
	}
	people, err := cache.SearchPeople(ctx, d.DB.R, row.ID, "", 0)
	if err != nil {
		return nil, err
	}
	var person *model.Person
	for i := range people {
		if people[i].AccountID == accountID {
			person = &people[i]
			break
		}
	}
	if person == nil {
		return nil, apperr.New(apperr.NotFound, "That person is not known yet. Search for them first.")
	}
	if _, _, err := d.Settings.AddSpeaker(ctx, standupID, *person); err != nil {
		return nil, err
	}
	return s.speakers(ctx, standupID)
}

func (s *ConfigService) RemoveSpeaker(ctx context.Context, standupID int64, accountID string) (ack SaveAck, err error) {
	defer s.d.guard("RemoveSpeaker", &err)
	a, err := s.d.Settings.RemoveSpeaker(ctx, standupID, accountID)
	if err != nil {
		return SaveAck{}, err
	}
	return saveAck(a), nil
}

func (s *ConfigService) speakers(ctx context.Context, standupID int64) ([]view.SpeakerConfig, error) {
	// speakers is the configured speaker list in persisted order.
	cfg, err := s.d.standupConfig(ctx, standupID)
	if err != nil {
		return nil, err
	}
	return cfg.Speakers, nil
}
