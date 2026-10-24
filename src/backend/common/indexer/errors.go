package indexer

import (
	"fmt"
	"time"
)

// ErrKind classifies indexer failures (architecture.md §4.5).
type ErrKind int

const (
	KindConfig ErrKind = iota
	KindParse
	KindAuth
	KindRateLimit
	KindNetwork
	KindInternal
)

func (k ErrKind) String() string {
	switch k {
	case KindConfig:
		return "Config"
	case KindParse:
		return "Parse"
	case KindAuth:
		return "Auth"
	case KindRateLimit:
		return "RateLimit"
	case KindNetwork:
		return "Network"
	case KindInternal:
		return "Internal"
	}
	return "Unknown"
}

// IndexerError is the domain error carried by every indexer failure.
// Message follows upstream IndexerException formatting:
//
//	Exception (<id>): <base message>
//
// Ported from: Jackett.Common/Exceptions/IndexerException.cs.
type IndexerError struct {
	IndexerID  string
	Kind       ErrKind
	Message    string
	RetryAfter time.Duration // valid for Kind == KindRateLimit
}

func (e *IndexerError) Error() string {
	return fmt.Sprintf("Exception (%s): %s", e.IndexerID, e.Message)
}

// TooManyRequestsError mirrors upstream TooManyRequestsException: it is
// raised by the client stack when a tracker responds 429 (orRetry-After is
// present on a failing response) and carries the retry delay.
//
// Ported from: Jackett.Common/Exceptions/TooManyRequestsException.cs.
type TooManyRequestsError struct {
	Message    string
	RetryAfter time.Duration
}

func (e *TooManyRequestsError) Error() string { return e.Message }
