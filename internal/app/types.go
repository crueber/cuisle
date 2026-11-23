// Package app holds the Wails services: the only API the UI sees (architecture.md §3). Services are thin: validate
// arguments, call domain packages, map errors (apperr), emit events.
//
// FROZEN CONTRACT: this file (bound types) and the method sets in services.go. Implementation: WP-32.
// Nothing imports app. Only app and cmd/cuisle import Wails.
package app

import (
	"github.com/crueber/cuisle/internal/buildinfo"
	"github.com/crueber/cuisle/internal/settings"
	"github.com/crueber/cuisle/internal/sink"
	"github.com/crueber/cuisle/internal/source"
	"github.com/crueber/cuisle/internal/view"
)

type (
	StandupPatch = settings.Patch
	UIStatePatch = settings.UIStatePatch
	UIState      = settings.UIState
	WindowState  = settings.WindowState
	BuildInfo    = buildinfo.Info
)

// Event names emitted only by the app (the sync ones live in internal/sync).
const (
	EvSinkDelivered = "sink:delivered"
	EvSinkFailed    = "sink:failed"
	EvDayRolled     = "day:rolled"
)

type SaveAck struct {
	Rev       int64 `json:"rev"`
	SavedAt   int64 `json:"savedAt"`
	NeedsSync bool  `json:"needsSync"`
}

type Bootstrap struct {
	Build      BuildInfo         `json:"build"`
	Settings   map[string]string `json:"settings"`
	Connection Connection        `json:"connection"`
	Standups   []view.StandupTab `json:"standups"`
	UI         UIState           `json:"ui"`
	View       *view.StandupView `json:"view"` // nil when there are no configured standups
	Sync       []view.SyncStatus `json:"sync"`
	Recovered  bool              `json:"recovered"` // the DB was recovered from corruption (settings-loss banner)
}

type Connection struct {
	State        string              `json:"state"` // ok|invalid|missing|unknown
	Source       string              `json:"source"`
	BaseURL      string              `json:"baseUrl"`
	Account      string              `json:"account"`
	DisplayName  string              `json:"displayName"`
	DevActivity  string              `json:"devActivity"` // ok|unavailable|unknown
	Capabilities source.Capabilities `json:"capabilities"`
}

type ConnectInput struct {
	Source  string `json:"source"` // "jira"
	BaseURL string `json:"baseUrl"`
	Account string `json:"account"`
	Secret  string `json:"secret"`
}

type UserOption struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
	Initials    string `json:"initials"`
	OnBoard     bool   `json:"onBoard"` // already a speaker
}

type Diagnostics struct {
	Build         BuildInfo `json:"build"`
	OS            string    `json:"os"`
	DataDir       string    `json:"dataDir"`
	LogDir        string    `json:"logDir"`
	DBBytes       int64     `json:"dbBytes"`
	SchemaVersion int       `json:"schemaVersion"`
	RecentRuns    []RunInfo `json:"recentRuns"`
}

type RunInfo struct {
	ID         int64    `json:"id"`
	StandupID  int64    `json:"standupId"`
	Mode       string   `json:"mode"`
	Trigger    string   `json:"trigger"`
	Status     string   `json:"status"`
	StartedAt  int64    `json:"startedAt"`
	FinishedAt int64    `json:"finishedAt"`
	Requests   int      `json:"requests"`
	ErrorCode  string   `json:"errorCode"`
	Warnings   []string `json:"warnings"`
}

type SinkKind struct {
	Kind         string            `json:"kind"`
	DisplayName  string            `json:"displayName"`
	Capabilities sink.Capabilities `json:"capabilities"`
	Schema       []sink.Field      `json:"schema"`
}

type SinkConfig struct {
	ID           int64             `json:"id"`
	Kind         string            `json:"kind"`
	Name         string            `json:"name"`
	StandupID    int64             `json:"standupId"` // 0 = all
	Config       map[string]string `json:"config"`
	HasSecret    bool              `json:"hasSecret"`
	SendOn       string            `json:"sendOn"` // manual|after_sync
	Enabled      bool              `json:"enabled"`
	LastDelivery *OutboxItem       `json:"lastDelivery"`
}

type SinkInput struct {
	ID        int64             `json:"id"`
	Kind      string            `json:"kind"`
	Name      string            `json:"name"`
	StandupID int64             `json:"standupId"`
	Config    map[string]string `json:"config"`
	Secret    *string           `json:"secret"` // nil = unchanged
	SendOn    string            `json:"sendOn"`
	Enabled   bool              `json:"enabled"`
}

type SendRequest = sink.Request

type OutboxItem struct {
	ID          int64  `json:"id"`
	SinkID      int64  `json:"sinkId"`
	StandupID   int64  `json:"standupId"`
	Cause       string `json:"cause"`
	PayloadKind string `json:"payloadKind"`
	Status      string `json:"status"`
	Attempts    int    `json:"attempts"`
	ErrorCode   string `json:"errorCode"`
	Error       string `json:"error"`
	Receipt     string `json:"receipt"`
	CreatedAt   int64  `json:"createdAt"`
	DeliveredAt int64  `json:"deliveredAt"`
}

type HistoryRange struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Event payloads emitted by the app.
type SinkDelivered struct {
	OutboxID  int64  `json:"outboxId"`
	SinkID    int64  `json:"sinkId"`
	StandupID int64  `json:"standupId"`
	Kind      string `json:"kind"`
	Ref       string `json:"ref"`
}

type SinkFailed struct {
	OutboxID     int64  `json:"outboxId"`
	SinkID       int64  `json:"sinkId"`
	StandupID    int64  `json:"standupId"`
	Kind         string `json:"kind"`
	ErrorCode    string `json:"errorCode"`
	ErrorMessage string `json:"errorMessage"`
	Attempts     int    `json:"attempts"`
	WillRetry    bool   `json:"willRetry"`
}

type DayRolled struct {
	Today string `json:"today"`
}
