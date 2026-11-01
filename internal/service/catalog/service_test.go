package catalog_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/xBlaz3kx/homebox-analytics/internal/service/catalog/mocks"

	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/barcode"
	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/homebox"
	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/search"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/catalog"
)

type deps struct {
	hb       *mocks.MockHomebox
	codes    *mocks.MockCodeRepository
	searcher *mocks.MockSearcher
	points   *mocks.MockPointStore
	svc      *catalog.Service
}

func newDeps(t *testing.T) deps {
	d := deps{
		hb:       mocks.NewMockHomebox(t),
		codes:    mocks.NewMockCodeRepository(t),
		searcher: mocks.NewMockSearcher(t),
		points:   mocks.NewMockPointStore(t),
	}
	svc, err := catalog.NewService(catalog.Config{
		CodeFields:     []string{"EAN"},
		EmbeddingModel: "nomic-embed-text",
		MinSimilarity:  0.75,
		MinMargin:      0.03,
	}, d.hb, d.codes, d.searcher, d.points)
	require.NoError(t, err)
	d.svc = svc
	return d
}

func payload(id, name string) map[string]any {
	return map[string]any{"item_id": id, "name": name, "tag_names": []any{"Food"}}
}

func TestResolveLearnedCode(t *testing.T) {
	d := newDeps(t)
	ctx := context.Background()

	d.codes.EXPECT().Find(mock.Anything, barcode.Code{Value: "SKU42", Type: barcode.TypeOther}, "shop").
		Return(&catalog.CodeLink{ItemID: "item-1"}, nil)
	d.points.EXPECT().Scroll(mock.Anything, &search.Filter{Field: "item_id", Any: []string{"item-1"}}).
		Return([]search.Hit{{ID: "item-1", Payload: payload("item-1", "Rice")}}, nil)

	res, err := d.svc.Resolve(ctx, catalog.LookupQuery{Code: "sku-42", Vendor: "shop"})
	require.NoError(t, err)
	assert.Equal(t, catalog.MatchExact, res.MatchType)
	assert.True(t, res.Confident)
	assert.Equal(t, "Rice", res.Candidates[0].Name)
	assert.Equal(t, []string{"Food"}, res.Candidates[0].Tags)
}

func TestResolveHomeboxCode(t *testing.T) {
	d := newDeps(t)

	d.codes.EXPECT().Find(mock.Anything, mock.Anything, "").Return(nil, catalog.ErrNotFound)
	d.points.EXPECT().Scroll(mock.Anything, &search.Filter{Field: "codes", Any: []string{"0036000291452"}}).
		Return([]search.Hit{{ID: "item-2", Payload: payload("item-2", "Soap")}}, nil)

	res, err := d.svc.Resolve(context.Background(), catalog.LookupQuery{Code: "036000291452"})
	require.NoError(t, err)
	assert.Equal(t, catalog.MatchExact, res.MatchType)
	assert.True(t, res.Confident)
	assert.Equal(t, "item-2", res.Candidates[0].ItemID)
}

func TestResolveHybridConfidence(t *testing.T) {
	tests := []struct {
		name      string
		sims      []float64
		confident bool
	}{
		{name: "clear winner", sims: []float64{0.9, 0.7}, confident: true},
		{name: "too close", sims: []float64{0.9, 0.89}, confident: false},
		{name: "too weak", sims: []float64{0.6}, confident: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newDeps(t)
			d.codes.EXPECT().Find(mock.Anything, mock.Anything, "").Return(nil, catalog.ErrNotFound)
			d.points.EXPECT().Scroll(mock.Anything, mock.Anything).Return(nil, nil)

			var hits []search.Hit
			for i, s := range tt.sims {
				hits = append(hits, search.Hit{ID: string(rune('a' + i)), Score: 1 / float64(i+1), Similarity: s, Payload: map[string]any{}})
			}
			d.searcher.EXPECT().Search(mock.Anything, "BARILLA SPAG 500G", (*search.Filter)(nil), 5).Return(hits, nil)

			res, err := d.svc.Resolve(context.Background(), catalog.LookupQuery{Code: "999-111", Text: "BARILLA SPAG 500G"})
			require.NoError(t, err)
			assert.Equal(t, catalog.MatchHybrid, res.MatchType)
			assert.Equal(t, tt.confident, res.Confident)
		})
	}
}

func TestResolveRequiresInput(t *testing.T) {
	d := newDeps(t)
	_, err := d.svc.Resolve(context.Background(), catalog.LookupQuery{})
	assert.ErrorIs(t, err, catalog.ErrEmptyQuery)
}

func TestSync(t *testing.T) {
	d := newDeps(t)
	ctx := context.Background()
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)

	entity := func(id string, updated time.Time, name string) *homebox.Entity {
		return &homebox.Entity{
			EntitySummary: homebox.EntitySummary{ID: id, Name: name, UpdatedAt: updated},
			Fields:        []homebox.Field{{Name: "ean", TextValue: "4006381333931"}},
		}
	}

	d.hb.EXPECT().ListAllEntities(mock.Anything, []string(nil)).Return([]homebox.EntitySummary{
		{ID: "same", UpdatedAt: t0},
		{ID: "qty-changed", UpdatedAt: t1},
		{ID: "renamed", UpdatedAt: t1},
		{ID: "new", UpdatedAt: t0},
		{ID: "archived", UpdatedAt: t0, Archived: true},
	}, nil)

	// Capture the content hash the current code computes for an unchanged "qty-changed" item.
	var qtyHash string
	d.searcher.EXPECT().Index(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, docs []search.Document) error {
		ids := make([]string, len(docs))
		for i, doc := range docs {
			ids[i] = doc.ID
			assert.Equal(t, []string{"4006381333931"}, doc.Payload["codes"])
		}
		assert.ElementsMatch(t, []string{"renamed", "new"}, ids)
		return nil
	}).Once()

	// Seed the index payloads by computing what the sync would write for the unchanged content.
	probe := newDeps(t)
	probe.hb.EXPECT().ListAllEntities(mock.Anything, mock.Anything).Return([]homebox.EntitySummary{{ID: "qty-changed", UpdatedAt: t0}}, nil)
	probe.points.EXPECT().Scroll(mock.Anything, mock.Anything).Return(nil, nil)
	probe.hb.EXPECT().GetEntity(mock.Anything, "qty-changed").Return(entity("qty-changed", t0, "Coffee"), nil)
	probe.searcher.EXPECT().Index(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, docs []search.Document) error {
		qtyHash = docs[0].Payload["content_hash"].(string)
		return nil
	})
	probe.points.EXPECT().Delete(mock.Anything, mock.Anything).Return(nil)
	_, err := probe.svc.Sync(ctx)
	require.NoError(t, err)

	d.points.EXPECT().Scroll(mock.Anything, (*search.Filter)(nil)).Return([]search.Hit{
		{ID: "same", Payload: map[string]any{"updated_at": t0.Format(time.RFC3339Nano), "embedding_model": "nomic-embed-text"}},
		{ID: "qty-changed", Payload: map[string]any{"updated_at": t0.Format(time.RFC3339Nano), "embedding_model": "nomic-embed-text", "content_hash": qtyHash}},
		{ID: "renamed", Payload: map[string]any{"updated_at": t0.Format(time.RFC3339Nano), "content_hash": "old"}},
		{ID: "gone", Payload: map[string]any{}},
	}, nil)
	d.hb.EXPECT().GetEntity(mock.Anything, "qty-changed").Return(entity("qty-changed", t1, "Coffee"), nil)
	d.hb.EXPECT().GetEntity(mock.Anything, "renamed").Return(entity("renamed", t1, "Better name"), nil)
	d.hb.EXPECT().GetEntity(mock.Anything, "new").Return(entity("new", t0, "New thing"), nil)
	d.points.EXPECT().SetPayload(mock.Anything, "qty-changed", map[string]any{"updated_at": t1.Format(time.RFC3339Nano)}).Return(nil)
	d.points.EXPECT().Delete(mock.Anything, []string{"gone"}).Return(nil)

	stats, err := d.svc.Sync(ctx)
	require.NoError(t, err)
	assert.Equal(t, 4, stats.Seen)
	assert.Equal(t, 1, stats.Unchanged)
	assert.Equal(t, 1, stats.Touched)
	assert.Equal(t, 2, stats.Upserted)
	assert.Equal(t, 1, stats.Deleted)
}

// The package tracer binds to the first global provider set, so this is the only test that installs one.
func TestResolveSpans(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))

	d := newDeps(t)
	d.codes.EXPECT().Find(mock.Anything, mock.Anything, "").Return(nil, catalog.ErrNotFound)
	d.points.EXPECT().Scroll(mock.Anything, mock.Anything).Return(nil, nil)
	d.searcher.EXPECT().Search(mock.Anything, "4006381333931", (*search.Filter)(nil), 5).
		Return([]search.Hit{{ID: "item-3", Similarity: 0.9, Payload: payload("item-3", "Pen")}}, nil)

	_, err := d.svc.Resolve(context.Background(), catalog.LookupQuery{Code: "4006381333931"})
	require.NoError(t, err)

	spans := map[string]sdktrace.ReadOnlySpan{}
	for _, s := range rec.Ended() {
		spans[s.Name()] = s
	}
	require.Contains(t, spans, "catalog.Resolve")
	require.Contains(t, spans, "catalog.exactMatch")
	require.Contains(t, spans, "catalog.hybridMatch")

	root := spans["catalog.Resolve"].SpanContext().SpanID()
	assert.Equal(t, root, spans["catalog.exactMatch"].Parent().SpanID())
	assert.Equal(t, root, spans["catalog.hybridMatch"].Parent().SpanID())
	assert.Contains(t, spans["catalog.exactMatch"].Attributes(), attribute.Bool("found", false))
	assert.Contains(t, spans["catalog.hybridMatch"].Attributes(), attribute.Bool("confident", true))
}
