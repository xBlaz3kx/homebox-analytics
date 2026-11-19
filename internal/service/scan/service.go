// Package scan resolves a scanned code or receipt line to a Homebox item and records a ledger
// event for it in one step. It is shared by the HTTP API and the MCP server.
package scan

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/observability"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/catalog"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/ledger"
)

var tracer = otel.Tracer("github.com/xBlaz3kx/homebox-analytics/internal/service/scan")

// ErrNoConfidentMatch is returned when the code/text did not resolve to a single item. The
// accompanying Result still carries the match so callers can offer the candidates.
var ErrNoConfidentMatch = errors.New("no confident match")

type Catalog interface {
	Resolve(ctx context.Context, q catalog.LookupQuery) (catalog.LookupResult, error)
	LinkCode(ctx context.Context, code, vendor, itemID string, source catalog.CodeSource) (*catalog.CodeLink, error)
}

type Ledger interface {
	RecordEvent(ctx context.Context, in ledger.EventInput) (*ledger.Event, error)
}

type Request struct {
	Code   string
	Vendor string
	Text   string
	// Learn links the code to the matched item when the match was hybrid but confident.
	Learn bool
	// Event is recorded against the matched item; ItemID is ignored.
	Event ledger.EventInput
}

type Result struct {
	Match catalog.LookupResult `json:"match"`
	Event *ledger.Event        `json:"event,omitempty"`
	Link  *catalog.CodeLink    `json:"link,omitempty"`
}

type Service struct {
	catalog Catalog
	ledger  Ledger
}

func NewService(c Catalog, l Ledger) *Service {
	return &Service{catalog: c, ledger: l}
}

// Scan resolves the request to an item and records the event. Without a confident match it returns
// ErrNoConfidentMatch together with the match result and records nothing.
func (s *Service) Scan(ctx context.Context, req Request) (res Result, err error) {
	ctx, span := tracer.Start(ctx, "scan.Scan")
	defer observability.End(span, &err)

	res.Match, err = s.catalog.Resolve(ctx, catalog.LookupQuery{Code: req.Code, Vendor: req.Vendor, Text: req.Text})
	if err != nil {
		return res, err
	}
	best, ok := res.Match.Best()
	if !ok {
		return res, ErrNoConfidentMatch
	}
	span.SetAttributes(attribute.String("item_id", best.ItemID))

	in := req.Event
	in.ItemID = best.ItemID
	if in.CodeUsed == "" {
		in.CodeUsed = req.Code
	}
	if in.Vendor == "" {
		in.Vendor = req.Vendor
	}

	if res.Event, err = s.ledger.RecordEvent(ctx, in); err != nil {
		return res, err
	}

	if req.Learn && req.Code != "" && res.Match.MatchType == catalog.MatchHybrid {
		if res.Link, err = s.catalog.LinkCode(ctx, req.Code, req.Vendor, best.ItemID, catalog.SourceLearned); err != nil {
			return res, err
		}
	}
	return res, nil
}
