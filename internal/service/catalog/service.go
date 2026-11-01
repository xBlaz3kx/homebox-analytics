package catalog

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"

	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/barcode"
	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/homebox"
	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/search"
)

var tracer = otel.Tracer("github.com/xBlaz3kx/homebox-analytics/internal/service/catalog")

// Homebox is the subset of the Homebox client the catalog needs.
type Homebox interface {
	ListAllEntities(ctx context.Context, tagIDs []string) ([]homebox.EntitySummary, error)
	GetEntity(ctx context.Context, id string) (*homebox.Entity, error)
}

// Searcher indexes documents and runs hybrid queries.
type Searcher interface {
	Index(ctx context.Context, docs []search.Document) error
	Search(ctx context.Context, text string, filter *search.Filter, limit int) ([]search.Hit, error)
}

// PointStore gives direct access to indexed points (scroll, payload updates, deletes).
type PointStore interface {
	Scroll(ctx context.Context, filter *search.Filter) ([]search.Hit, error)
	SetPayload(ctx context.Context, id string, payload map[string]any) error
	Delete(ctx context.Context, ids []string) error
}

type Config struct {
	CodeFields     []string
	EmbeddingModel string
	MinSimilarity  float64
	MinMargin      float64
}

type Service struct {
	cfg      Config
	homebox  Homebox
	codes    CodeRepository
	searcher Searcher
	points   PointStore
	metrics  *metrics

	// syncMu serializes syncs triggered by the ticker and the API.
	syncMu sync.Mutex
}

func NewService(cfg Config, hb Homebox, codes CodeRepository, searcher Searcher, points PointStore) (*Service, error) {
	m, err := newMetrics(otel.Meter("homebox-analytics"))
	if err != nil {
		return nil, err
	}
	return &Service{cfg: cfg, homebox: hb, codes: codes, searcher: searcher, points: points, metrics: m}, nil
}

// Resolve finds the item for a code and/or text: learned code links first, then codes stored on
// Homebox items, then hybrid (semantic + lexical) search.
func (s *Service) Resolve(ctx context.Context, q LookupQuery) (res LookupResult, err error) {
	ctx, span := tracer.Start(ctx, "catalog.Resolve")
	defer func() {
		span.SetAttributes(
			attribute.String("match_type", string(res.MatchType)),
			attribute.Bool("confident", res.Confident),
			attribute.Int("candidates", len(res.Candidates)),
		)
		if len(res.Candidates) > 0 {
			span.SetAttributes(attribute.Float64("top_score", res.Candidates[0].Score))
		}
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else {
			s.metrics.lookups.Add(ctx, 1, metric.WithAttributes(
				attribute.String("match_type", string(res.MatchType)),
				attribute.Bool("confident", res.Confident),
			))
		}
		span.End()
	}()

	if q.Code == "" && q.Text == "" {
		return LookupResult{}, ErrEmptyQuery
	}
	if q.Limit <= 0 {
		q.Limit = 5
	}

	if q.Code != "" {
		code := barcode.Normalize(q.Code)
		span.SetAttributes(attribute.String("code_type", string(code.Type)), attribute.String("vendor", q.Vendor))

		res, found, err := s.exactMatch(ctx, code, q.Vendor)
		if err != nil || found {
			return res, err
		}
	}

	text := q.Text
	if text == "" {
		text = q.Code
	}
	return s.hybridMatch(ctx, text, q.Limit)
}

func (s *Service) exactMatch(ctx context.Context, code barcode.Code, vendor string) (LookupResult, bool, error) {
	res := LookupResult{MatchType: MatchExact, Code: &code}

	link, err := s.codes.Find(ctx, code, vendor)
	switch {
	case err == nil:
		hits, err := s.points.Scroll(ctx, &search.Filter{Field: fieldItemID, Any: []string{link.ItemID}})
		if err != nil {
			return res, false, fmt.Errorf("load linked item: %w", err)
		}
		if len(hits) == 0 {
			// The linked item may not be indexed yet; return the bare ID.
			hits = []search.Hit{{ID: link.ItemID, Payload: map[string]any{fieldItemID: link.ItemID}}}
		}
		res.Candidates = []Candidate{toCandidate(hits[0], 1)}
		res.Confident = true
		return res, true, nil
	case !errors.Is(err, ErrNotFound):
		return res, false, fmt.Errorf("find code link: %w", err)
	}

	hits, err := s.points.Scroll(ctx, &search.Filter{Field: fieldCodes, Any: []string{code.Value}})
	if err != nil {
		return res, false, fmt.Errorf("find code in index: %w", err)
	}
	if len(hits) == 0 {
		return res, false, nil
	}

	for _, h := range hits {
		res.Candidates = append(res.Candidates, toCandidate(h, 1))
	}
	res.Confident = len(hits) == 1
	return res, true, nil
}

func (s *Service) hybridMatch(ctx context.Context, text string, limit int) (LookupResult, error) {
	start := time.Now()
	hits, err := s.searcher.Search(ctx, text, nil, limit)
	s.metrics.searchDuration.Record(ctx, time.Since(start).Seconds())
	if err != nil {
		return LookupResult{}, fmt.Errorf("hybrid search: %w", err)
	}
	if len(hits) == 0 {
		return LookupResult{MatchType: MatchNone, Candidates: []Candidate{}}, nil
	}

	res := LookupResult{MatchType: MatchHybrid}
	for _, h := range hits {
		res.Candidates = append(res.Candidates, toCandidate(h, h.Score))
	}

	top := hits[0].Similarity
	res.Confident = top >= s.cfg.MinSimilarity && (len(hits) == 1 || top-hits[1].Similarity >= s.cfg.MinMargin)
	return res, nil
}

func toCandidate(h search.Hit, score float64) Candidate {
	c := Candidate{ItemID: h.ID, Score: score, Similarity: h.Similarity, Tags: []string{}}
	if id, ok := h.Payload[fieldItemID].(string); ok && id != "" {
		c.ItemID = id
	}
	c.Name, _ = h.Payload[fieldName].(string)
	c.Location, _ = h.Payload[fieldLocation].(string)
	if names, ok := h.Payload[fieldTagNames].([]any); ok {
		for _, n := range names {
			if s, ok := n.(string); ok {
				c.Tags = append(c.Tags, s)
			}
		}
	}
	return c
}

// LinkCode stores a code -> item mapping so future lookups of this code are exact.
func (s *Service) LinkCode(ctx context.Context, rawCode, vendor, itemID string, source CodeSource) (*CodeLink, error) {
	code := barcode.Normalize(rawCode)
	if code.Value == "" || itemID == "" {
		return nil, errors.New("code and item_id are required")
	}
	if !slices.Contains([]CodeSource{SourceLearned, SourceManual}, source) {
		return nil, fmt.Errorf("invalid source %q", source)
	}

	if _, err := s.homebox.GetEntity(ctx, itemID); err != nil {
		if errors.Is(err, homebox.ErrNotFound) {
			return nil, fmt.Errorf("item %s: %w", itemID, ErrNotFound)
		}
		return nil, fmt.Errorf("verify item: %w", err)
	}

	return s.codes.Upsert(ctx, CodeLink{Code: code.Value, CodeType: code.Type, ItemID: itemID, Source: source, Vendor: vendor})
}

func (s *Service) ListCodes(ctx context.Context, itemID string) ([]CodeLink, error) {
	return s.codes.ListByItem(ctx, itemID)
}

func (s *Service) DeleteCode(ctx context.Context, id string) error {
	return s.codes.Delete(ctx, id)
}
