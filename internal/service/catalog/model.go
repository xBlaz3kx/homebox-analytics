package catalog

import (
	"errors"
	"time"

	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/barcode"
)

type MatchType string

const (
	MatchExact  MatchType = "exact"
	MatchHybrid MatchType = "hybrid"
	MatchNone   MatchType = "none"
)

type CodeSource string

const (
	SourceLearned CodeSource = "learned"
	SourceManual  CodeSource = "manual"
)

var (
	ErrNotFound   = errors.New("not found")
	ErrEmptyQuery = errors.New("code or text is required")
)

// LookupQuery identifies an item by a scanned/entered code and/or free text (e.g. a receipt line).
type LookupQuery struct {
	Code   string
	Vendor string
	Text   string
	Limit  int
}

type Candidate struct {
	ItemID     string   `json:"item_id"`
	Name       string   `json:"name"`
	Tags       []string `json:"tags"`
	Location   string   `json:"location,omitempty"`
	Score      float64  `json:"score"`
	Similarity float64  `json:"similarity,omitempty"`
}

type LookupResult struct {
	MatchType  MatchType     `json:"match_type"`
	Confident  bool          `json:"confident"`
	Code       *barcode.Code `json:"code,omitempty"`
	Candidates []Candidate   `json:"candidates"`
}

// Best returns the top candidate when the match is confident.
func (r LookupResult) Best() (Candidate, bool) {
	if !r.Confident || len(r.Candidates) == 0 {
		return Candidate{}, false
	}
	return r.Candidates[0], true
}

// CodeLink maps a (vendor-scoped) code to a Homebox item.
type CodeLink struct {
	ID        string       `json:"id"`
	Code      string       `json:"code"`
	CodeType  barcode.Type `json:"code_type"`
	ItemID    string       `json:"item_id"`
	Source    CodeSource   `json:"source"`
	Vendor    string       `json:"vendor,omitempty"`
	CreatedAt time.Time    `json:"created_at"`
}

type SyncStats struct {
	Seen      int           `json:"seen"`
	Upserted  int           `json:"upserted"`
	Touched   int           `json:"touched"`
	Unchanged int           `json:"unchanged"`
	Deleted   int           `json:"deleted"`
	Duration  time.Duration `json:"duration_ns"`
}
