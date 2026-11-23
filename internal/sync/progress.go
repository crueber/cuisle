package sync

import (
	gosync "sync"
	"time"
)

// progressReporter publishes sync:progress for one run. Intermediate counts inside one phase are coalesced by the time
// since the last emitted event (no ticker, no goroutine); a phase change and the completion of a phase always emit.
type progressReporter struct {
	o        *Orchestrator
	r        *run
	interval time.Duration

	mu        gosync.Mutex
	lastPhase string
	lastEmit  time.Time
}

func newProgressReporter(o *Orchestrator, r *run) *progressReporter {
	return &progressReporter{o: o, r: r, interval: o.opts.ProgressInterval}
}

// report records the live status and emits an event unless it is an intermediate count inside the coalescing window.
func (p *progressReporter) report(phase string, done, total int) {
	if total < 0 {
		total = 0
	}
	if done < 0 {
		done = 0
	}
	if done > total {
		done = total
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	p.o.mu.Lock()
	p.r.status.Phase, p.r.status.Done, p.r.status.Total = phase, done, total
	p.o.mu.Unlock()

	now := time.Now()
	if phase == p.lastPhase && done < total && now.Sub(p.lastEmit) < p.interval {
		return
	}
	p.lastPhase, p.lastEmit = phase, now
	p.o.emit(EvSyncProgress, SyncProgress{RunID: p.r.id, StandupID: p.r.standupID, Phase: phase, Done: done, Total: total})
}
