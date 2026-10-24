package indexer

import (
	"context"
	"net/url"

	"vestt/common/httpclient"
	"vestt/common/models"
)

// ConfigStatus mirrors upstream IndexerConfigurationStatus
// (Jackett.Common/Models/IndexerConfigurationStatus.cs).
type ConfigStatus int

const (
	// ConfigCompleted: configuration applied, no test needed.
	ConfigCompleted ConfigStatus = iota
	// ConfigRequiresTesting: configuration applied; the manager must run
	// Test() afterwards (upstream UpdateConfig → TestIndexer).
	ConfigRequiresTesting
)

// IndexerResult mirrors upstream IndexerResult (release set + elapsed ms +
// cache flag).
type IndexerResult struct {
	Indexer     Indexer
	Releases    []*models.ReleaseInfo
	ElapsedTime int64 // milliseconds
	IsFromCache bool
}

// Indexer is the upstream IIndexer equivalent (architecture.md §4.2).
type Indexer interface {
	// identity
	ID() string
	Name() string
	Description() string
	SiteLink() string
	AlternativeSiteLinks() []string
	Language() string
	Type() string // public | semi-private | private
	Tags() []string

	// state
	IsConfigured() bool
	LastError() string
	SetLastError(msg string)
	IsHealthy() bool
	IsFailing() bool

	Capabilities() *models.Capabilities
	CanHandleQuery(q *models.TorznabQuery) bool
	ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*IndexerResult, error)

	// management
	ConfigurationForSetup(ctx context.Context) ([]map[string]any, error)
	ApplyConfiguration(ctx context.Context, items []ConfigItem) (ConfigStatus, error)
	LoadFromSavedConfiguration(ctx context.Context, raw []byte) error
	Unconfigure(ctx context.Context) error
	Test(ctx context.Context) error

	// download proxying
	Download(ctx context.Context, link *url.URL) ([]byte, error)
	DownloadImage(ctx context.Context, link *url.URL) (*httpclient.Result, error)
}
