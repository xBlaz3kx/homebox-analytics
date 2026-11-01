//go:build integration

package service_test

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcqdrant "github.com/testcontainers/testcontainers-go/modules/qdrant"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/zap"

	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/database"
	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/homebox"
	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/qdrant"
	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/search"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/analytics"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/catalog"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/ledger"
)

// fakeHomebox serves the subset of the Homebox API the service uses, backed by memory.
type fakeHomebox struct {
	mu       sync.Mutex
	entities map[string]*homebox.Entity
	tags     []homebox.Tag
}

func (f *fakeHomebox) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/entities", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		items := []homebox.EntitySummary{}
		for _, e := range f.entities {
			items = append(items, e.EntitySummary)
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page > 1 {
			items = nil
		}
		_ = json.NewEncoder(w).Encode(homebox.EntityPage{Items: items, Total: len(f.entities)})
	})
	mux.HandleFunc("GET /api/v1/entities/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		e, ok := f.entities[r.PathValue("id")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(e)
	})
	mux.HandleFunc("PATCH /api/v1/entities/{id}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Quantity float64 `json:"quantity"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		e := f.entities[r.PathValue("id")]
		e.Quantity = body.Quantity
		e.UpdatedAt = time.Now()
		_ = json.NewEncoder(w).Encode(e)
	})
	mux.HandleFunc("GET /api/v1/tags", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(f.tags)
	})
	return mux
}

func (f *fakeHomebox) quantity(id string) float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.entities[id].Quantity
}

// countingEmbedder hashes tokens into a fixed-size bag-of-words vector, so texts sharing words are similar.
type countingEmbedder struct {
	mu    sync.Mutex
	calls int
	texts int
}

func (e *countingEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	e.mu.Lock()
	e.calls++
	e.texts += len(texts)
	e.mu.Unlock()

	enc := search.NewEncoder()
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, 64)
		for _, tok := range enc.Tokens(t) {
			h := fnv.New32a()
			_, _ = h.Write([]byte(tok))
			v[h.Sum32()%64]++
		}
		v[63] += 0.01 // avoid zero vectors
		out[i] = v
	}
	return out, nil
}

func TestEndToEnd(t *testing.T) {
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("hba"), tcpostgres.WithUsername("hba"), tcpostgres.WithPassword("hba"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(time.Minute)))
	require.NoError(t, err)
	testcontainers.CleanupContainer(t, pg)

	qd, err := tcqdrant.Run(ctx, "qdrant/qdrant:v1.15.5")
	require.NoError(t, err)
	testcontainers.CleanupContainer(t, qd)

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	pool, err := database.Connect(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.NoError(t, database.Migrate(ctx, pool))

	grpcEndpoint, err := qd.GRPCEndpoint(ctx)
	require.NoError(t, err)
	host, portStr, err := net.SplitHostPort(grpcEndpoint)
	require.NoError(t, err)
	port, _ := strconv.Atoi(portStr)

	index, err := qdrant.New(qdrant.Config{Host: host, Port: port, Collection: "items"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = index.Close() })
	require.NoError(t, index.EnsureCollection(ctx, 64, catalog.KeywordFields))
	// Re-running is idempotent and validates the dimension.
	require.NoError(t, index.EnsureCollection(ctx, 64, catalog.KeywordFields))
	require.ErrorIs(t, index.EnsureCollection(ctx, 128, catalog.KeywordFields), qdrant.ErrDimensionMismatch)

	now := time.Now().UTC()
	food := homebox.Tag{ID: "tag-food", Name: "Food"}
	cleaning := homebox.Tag{ID: "tag-cleaning", Name: "Cleaning"}
	fake := &fakeHomebox{
		tags: []homebox.Tag{food, cleaning},
		entities: map[string]*homebox.Entity{
			"spaghetti": {
				EntitySummary: homebox.EntitySummary{ID: "spaghetti", Name: "Barilla Spaghetti n.5 500g", Quantity: 2, Tags: []homebox.Tag{food}, UpdatedAt: now},
				Manufacturer:  "Barilla",
				Fields:        []homebox.Field{{Name: "EAN", TextValue: "8076800195057"}},
			},
			"detergent": {
				EntitySummary: homebox.EntitySummary{ID: "detergent", Name: "Dish detergent lemon 1L", Quantity: 1, Tags: []homebox.Tag{cleaning}, UpdatedAt: now},
			},
			"rice": {
				EntitySummary: homebox.EntitySummary{ID: "rice", Name: "Basmati rice 1kg", Quantity: 0, Tags: []homebox.Tag{food}, UpdatedAt: now},
			},
		},
	}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	hb, err := homebox.New(homebox.Config{URL: srv.URL, APIKey: "test"})
	require.NoError(t, err)

	embedder := &countingEmbedder{}
	cat, err := catalog.NewService(catalog.Config{
		CodeFields: []string{"EAN"}, EmbeddingModel: "fake", MinSimilarity: 0.5, MinMargin: 0.01,
	}, hb, catalog.NewPostgresCodeRepository(pool), search.NewHybridSearch(embedder, search.NewEncoder(), index), index)
	require.NoError(t, err)

	led, err := ledger.NewService(ledger.NewPostgresRepository(pool), hb, zap.NewNop())
	require.NoError(t, err)
	an := analytics.NewService(analytics.NewPostgresRepository(pool), hb)

	t.Run("sync", func(t *testing.T) {
		stats, err := cat.Sync(ctx)
		require.NoError(t, err)
		assert.Equal(t, 3, stats.Upserted)

		before := embedder.texts
		stats, err = cat.Sync(ctx)
		require.NoError(t, err)
		assert.Equal(t, 3, stats.Unchanged)
		assert.Equal(t, before, embedder.texts, "second sync must not re-embed")
	})

	t.Run("lookup", func(t *testing.T) {
		res, err := cat.Resolve(ctx, catalog.LookupQuery{Code: "8076800195057"})
		require.NoError(t, err)
		assert.Equal(t, catalog.MatchExact, res.MatchType)
		assert.Equal(t, "spaghetti", res.Candidates[0].ItemID)
		assert.Equal(t, []string{"Food"}, res.Candidates[0].Tags)

		res, err = cat.Resolve(ctx, catalog.LookupQuery{Code: "V-7781", Vendor: "mercator", Text: "BARILLA SPAGHETTI 0,5KG"})
		require.NoError(t, err)
		assert.Equal(t, catalog.MatchHybrid, res.MatchType)
		require.NotEmpty(t, res.Candidates)
		assert.Equal(t, "spaghetti", res.Candidates[0].ItemID)

		_, err = cat.LinkCode(ctx, "V-7781", "mercator", "spaghetti", catalog.SourceLearned)
		require.NoError(t, err)

		res, err = cat.Resolve(ctx, catalog.LookupQuery{Code: "v7781", Vendor: "mercator"})
		require.NoError(t, err)
		assert.Equal(t, catalog.MatchExact, res.MatchType)
		assert.Equal(t, "spaghetti", res.Candidates[0].ItemID)
	})

	t.Run("ledger", func(t *testing.T) {
		price := 1.29
		_, err := led.RecordEvent(ctx, ledger.EventInput{ItemID: "spaghetti", Type: ledger.EventPurchase, Quantity: 4, UnitPrice: &price, OccurredAt: now.AddDate(0, -2, 0)})
		require.NoError(t, err)
		_, err = led.RecordEvent(ctx, ledger.EventInput{ItemID: "spaghetti", Type: ledger.EventConsume, Quantity: 3, OccurredAt: now.AddDate(0, -1, 0)})
		require.NoError(t, err)
		_, err = led.RecordEvent(ctx, ledger.EventInput{ItemID: "detergent", Type: ledger.EventConsume, Quantity: 1, OccurredAt: now.AddDate(0, -1, 0)})
		require.NoError(t, err)
		assert.Equal(t, 3.0, fake.quantity("spaghetti"))

		_, err = led.RecordEvent(ctx, ledger.EventInput{ItemID: "rice", Type: ledger.EventConsume, Quantity: 1})
		assert.ErrorIs(t, err, ledger.ErrInsufficientQuantity)

		rec, err := led.Reconcile(ctx, ledger.ReconcileInput{ItemID: "spaghetti", Counted: 2, Apply: true})
		require.NoError(t, err)
		assert.Equal(t, -1.0, rec.Delta)
		assert.Equal(t, 2.0, fake.quantity("spaghetti"))

		events, err := led.ListEvents(ctx, ledger.EventFilter{ItemID: "spaghetti"})
		require.NoError(t, err)
		assert.Len(t, events, 3)
	})

	t.Run("charts", func(t *testing.T) {
		total := func(s analytics.Series) float64 {
			var sum float64
			for _, p := range s.Points {
				sum += p.V
			}
			return sum
		}

		chart, err := an.Series(ctx, analytics.Query{Categories: []string{analytics.AllCategories}, Interval: analytics.IntervalMonth})
		require.NoError(t, err)
		require.Len(t, chart.Series, 2)
		assert.Equal(t, "Cleaning", chart.Series[0].Name)
		assert.Equal(t, 1.0, total(chart.Series[0]))
		assert.Equal(t, "Food", chart.Series[1].Name)
		assert.Equal(t, 4.0, total(chart.Series[1]), "consumption includes negative reconciliation")

		chart, err = an.Series(ctx, analytics.Query{Items: []string{"spaghetti"}, Metric: analytics.MetricSpend, Interval: analytics.IntervalQuarter})
		require.NoError(t, err)
		require.Len(t, chart.Series, 1)
		assert.InDelta(t, 4*1.29, total(chart.Series[0]), 1e-9)

		chart, err = an.Series(ctx, analytics.Query{Items: []string{"spaghetti"}, Metric: analytics.MetricQuantity, Interval: analytics.IntervalMonth})
		require.NoError(t, err)
		pts := chart.Series[0].Points
		assert.Equal(t, 2.0, pts[len(pts)-1].V, "latest bucket reflects reconciled quantity")

		chart, err = an.Series(ctx, analytics.Query{Items: []string{"spaghetti", "detergent"}, GroupBy: analytics.GroupByTotal, Interval: analytics.IntervalYear})
		require.NoError(t, err)
		require.Len(t, chart.Series, 1)
		assert.Equal(t, 5.0, total(chart.Series[0]))
		assert.False(t, math.IsNaN(total(chart.Series[0])))
	})
}
