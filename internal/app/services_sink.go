package app

import (
	"context"
	"errors"
	"sync"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/secrets"
	"github.com/crueber/cuisle/internal/sink"
	"github.com/crueber/cuisle/internal/store"
)

// SinkService keeps a per-service cache of whether each sink has a keychain secret. Save and Delete keep it current,
// and List reads the keychain only for a sink of a kind that NeedsSecret and has no cached bit, so listing does not
// prompt for keychain access on every call. The cache lives on the service, so it is released with its Deps. Only a
// successful read or an explicit not-found is cached: an unavailable keychain is retried on the next List.
type SinkService struct {
	d *Deps

	mu   sync.Mutex
	bits map[int64]bool
}

// maxSecretBits bounds the cache; there are never this many sinks in practice.
const maxSecretBits = 1024

func (s *SinkService) cachedSecret(id int64) (has, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	has, ok = s.bits[id]
	return has, ok
}

func (s *SinkService) setSecretBit(id int64, has bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bits == nil || len(s.bits) >= maxSecretBits {
		s.bits = map[int64]bool{}
	}
	s.bits[id] = has
}

func (s *SinkService) forgetSecretBit(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.bits, id)
}

const (
	defaultOutboxLimit = 20
	maxOutboxLimit     = 200
)

func (s *SinkService) Kinds(ctx context.Context) (out []SinkKind, err error) {
	defer s.d.guard("Kinds", &err)
	kinds := sink.Kinds()
	out = make([]SinkKind, 0, len(kinds))
	for _, k := range kinds {
		caps := k.Capabilities()
		if caps.Formats == nil {
			caps.Formats = []string{}
		}
		schema := make([]sink.Field, 0, len(k.ConfigSchema()))
		for _, f := range k.ConfigSchema() {
			if f.Options == nil {
				f.Options = []string{}
			}
			schema = append(schema, f)
		}
		out = append(out, SinkKind{Kind: k.Kind(), DisplayName: k.DisplayName(), Capabilities: caps, Schema: schema})
	}
	return out, nil
}

func (s *SinkService) List(ctx context.Context) (out []SinkConfig, err error) {
	defer s.d.guard("List", &err)
	cfgs, err := sink.ListConfigs(ctx, s.d.DB)
	if err != nil {
		return nil, err
	}
	out = make([]SinkConfig, 0, len(cfgs))
	for _, c := range cfgs {
		sc, err := s.sinkConfig(ctx, c)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, nil
}

func (s *SinkService) Save(ctx context.Context, in SinkInput) (out SinkConfig, err error) {
	defer s.d.guard("Save", &err)
	d := s.d
	if in.StandupID != 0 {
		if _, err := d.loadLive(ctx, in.StandupID); err != nil {
			return SinkConfig{}, apperr.Validationf("standupId", "Choose a standup that exists.")
		}
	}
	c, err := sink.SaveConfig(ctx, d.DB, d.Keyring, sink.ConfigInput{
		ID: in.ID, Kind: in.Kind, Name: in.Name, StandupID: in.StandupID, Config: in.Config, Secret: in.Secret,
		SendOn: in.SendOn, Enabled: in.Enabled,
	})
	if err != nil {
		return SinkConfig{}, err
	}
	s.setSecretBit(c.ID, c.HasSecret)
	return s.sinkConfig(ctx, c)
}

func (s *SinkService) Delete(ctx context.Context, sinkID int64) (err error) {
	defer s.d.guard("Delete", &err)
	if err := sink.DeleteConfig(ctx, s.d.DB, s.d.Keyring, sinkID); err != nil {
		return err
	}
	s.forgetSecretBit(sinkID)
	return nil
}

func (s *SinkService) Send(ctx context.Context, sinkID int64, standupID int64, req SendRequest) (id int64, err error) {
	defer s.d.guard("Send", &err)
	// Send freezes the payload in the outbox and runs one delivery job before returning the outbox id. A delivery that
	// fails is recorded in the outbox (Retry, or the next explicit trigger), not returned.
	d := s.d
	if req.Kind != "current" && req.Kind != "range" {
		return 0, apperr.Validationf("kind", "Choose current or range.")
	}
	cfg, err := s.find(ctx, sinkID)
	if err != nil {
		return 0, err
	}
	kind, ok := sink.Get(cfg.Kind)
	if !ok {
		return 0, apperr.New(apperr.Unsupported, "This sink type is not available in this build.")
	}
	caps := kind.Capabilities()
	if req.Kind == "range" && !caps.Range || req.Kind == "current" && !caps.Current {
		return 0, apperr.New(apperr.Unsupported, "This destination cannot send that kind of export.")
	}
	if _, err := d.loadLive(ctx, standupID); err != nil {
		return 0, err
	}
	id, err = d.Sinks.Enqueue(ctx, sinkID, standupID, req, "manual", "")
	if err != nil {
		return 0, apperr.From(err)
	}
	return id, nil
}

func (s *SinkService) Outbox(ctx context.Context, sinkID int64, limit int) (out []OutboxItem, err error) {
	defer s.d.guard("Outbox", &err)
	return s.outbox(ctx, sinkID, limit)
}

func (s *SinkService) Retry(ctx context.Context, outboxID int64) (err error) {
	defer s.d.guard("Retry", &err)
	if err := s.d.Sinks.Retry(ctx, outboxID); err != nil {
		return apperr.From(err)
	}
	return nil
}

func (s *SinkService) find(ctx context.Context, sinkID int64) (sink.Config, error) {
	cfgs, err := sink.ListConfigs(ctx, s.d.DB)
	if err != nil {
		return sink.Config{}, err
	}
	for _, c := range cfgs {
		if c.ID == sinkID {
			return c, nil
		}
	}
	return sink.Config{}, apperr.New(apperr.NotFound, "That destination no longer exists.")
}

func (s *SinkService) outbox(ctx context.Context, sinkID int64, limit int) ([]OutboxItem, error) {
	if limit <= 0 {
		limit = defaultOutboxLimit
	}
	limit = min(limit, maxOutboxLimit)
	items, err := sink.Outbox(ctx, s.d.DB, sinkID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]OutboxItem, 0, len(items))
	for _, it := range items {
		out = append(out, outboxItem(it))
	}
	return out, nil
}

func (s *SinkService) sinkConfig(ctx context.Context, c sink.Config) (SinkConfig, error) {
	// sinkConfig maps a stored sink; HasSecret comes from the per-process bit (see SinkService). The secret itself
	// never leaves the keychain.
	cfg := c.Config
	if cfg == nil {
		cfg = map[string]string{}
	}
	hasSecret, known := s.cachedSecret(c.ID)
	if !known {
		if k, ok := sink.Get(c.Kind); ok && k.Capabilities().NeedsSecret {
			_, kerr := s.d.Keyring.Get(secrets.SinkAccount(c.ID))
			hasSecret = kerr == nil
			// Cache a success or an explicit not-found only; any other keychain error is retried next time.
			if kerr == nil || errors.Is(kerr, secrets.ErrNotFound) {
				s.setSecretBit(c.ID, hasSecret)
			}
		}
	}
	out := SinkConfig{
		ID: c.ID, Kind: c.Kind, Name: c.Name, StandupID: c.StandupID, Config: cfg, HasSecret: hasSecret,
		SendOn: c.SendOn, Enabled: c.Enabled,
	}
	last, err := s.outbox(ctx, c.ID, 1)
	if err != nil {
		return SinkConfig{}, err
	}
	if len(last) > 0 {
		out.LastDelivery = &last[0]
	}
	return out, nil
}

func outboxItem(it sink.OutboxItem) OutboxItem {
	return OutboxItem{
		ID: it.ID, SinkID: it.SinkID, StandupID: it.StandupID, Cause: it.Cause, PayloadKind: it.PayloadKind,
		Status: it.Status, Attempts: it.Attempts, ErrorCode: it.ErrorCode, Error: it.Error, Receipt: it.Receipt,
		CreatedAt: store.Ms(it.CreatedAt), DeliveredAt: store.Ms(it.DeliveredAt),
	}
}
