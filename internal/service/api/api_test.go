package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/xBlaz3kx/homebox-analytics/internal/service/analytics"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/catalog"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/ledger"
)

type fakeCatalog struct {
	Catalog
	result catalog.LookupResult
	linked []string
}

func (f *fakeCatalog) Resolve(context.Context, catalog.LookupQuery) (catalog.LookupResult, error) {
	return f.result, nil
}

func (f *fakeCatalog) LinkCode(_ context.Context, code, vendor, itemID string, _ catalog.CodeSource) (*catalog.CodeLink, error) {
	f.linked = append(f.linked, code+"@"+vendor+"->"+itemID)
	return &catalog.CodeLink{Code: code, ItemID: itemID}, nil
}

type fakeLedger struct {
	Ledger
	got []ledger.EventInput
	err error
}

func (f *fakeLedger) RecordEvent(_ context.Context, in ledger.EventInput) (*ledger.Event, error) {
	f.got = append(f.got, in)
	if f.err != nil {
		return nil, f.err
	}
	return &ledger.Event{ItemID: in.ItemID, Type: in.Type}, nil
}

type fakeAnalytics struct {
	got analytics.Query
}

func (f *fakeAnalytics) Series(_ context.Context, q analytics.Query) (*analytics.Chart, error) {
	f.got = q
	if len(q.Items) == 0 && len(q.Categories) == 0 {
		return nil, &analytics.ValidationError{Msg: "at least one of items or categories is required"}
	}
	return &analytics.Chart{Series: []analytics.Series{}}, nil
}

func do(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestScan(t *testing.T) {
	hybrid := catalog.LookupResult{MatchType: catalog.MatchHybrid, Confident: true, Candidates: []catalog.Candidate{{ItemID: "rice"}}}

	t.Run("confident match records event and learns code", func(t *testing.T) {
		c, l := &fakeCatalog{result: hybrid}, &fakeLedger{}
		h := NewHandler(c, l, &fakeAnalytics{}, nil, zap.NewNop())

		rec := do(t, h, http.MethodPost, "/api/v1/scan", `{"code":"V-1","vendor":"shop","text":"RICE 1KG","learn":true,"type":"purchase","quantity":2}`)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		require.Len(t, l.got, 1)
		assert.Equal(t, "rice", l.got[0].ItemID)
		assert.Equal(t, "V-1", l.got[0].CodeUsed)
		assert.Equal(t, "shop", l.got[0].Vendor)
		assert.Equal(t, []string{"V-1@shop->rice"}, c.linked)
	})

	t.Run("no confident match returns candidates", func(t *testing.T) {
		weak := hybrid
		weak.Confident = false
		l := &fakeLedger{}
		h := NewHandler(&fakeCatalog{result: weak}, l, &fakeAnalytics{}, nil, zap.NewNop())

		rec := do(t, h, http.MethodPost, "/api/v1/scan", `{"text":"rice","type":"consume","quantity":1}`)
		assert.Equal(t, http.StatusConflict, rec.Code)
		assert.Contains(t, rec.Body.String(), `"candidates"`)
		assert.Empty(t, l.got)
	})

	t.Run("insufficient quantity is a conflict", func(t *testing.T) {
		h := NewHandler(&fakeCatalog{result: hybrid}, &fakeLedger{err: ledger.ErrInsufficientQuantity}, &fakeAnalytics{}, nil, zap.NewNop())
		rec := do(t, h, http.MethodPost, "/api/v1/scan", `{"text":"rice","type":"consume","quantity":5}`)
		assert.Equal(t, http.StatusConflict, rec.Code)
	})
}

func TestChartQueryParsing(t *testing.T) {
	a := &fakeAnalytics{}
	h := NewHandler(&fakeCatalog{}, &fakeLedger{}, a, nil, zap.NewNop())

	rec := do(t, h, http.MethodGet, "/api/v1/charts?items=a,b&items=c&categories=all&group_by=category&metric=spend&interval=quarter&from=2026-01-01&timezone=Europe/Ljubljana", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []string{"a", "b", "c"}, a.got.Items)
	assert.Equal(t, []string{"all"}, a.got.Categories)
	assert.Equal(t, analytics.GroupByCategory, a.got.GroupBy)
	assert.Equal(t, analytics.MetricSpend, a.got.Metric)
	assert.Equal(t, analytics.IntervalQuarter, a.got.Interval)
	assert.Equal(t, "Europe/Ljubljana", a.got.Timezone)
	assert.Equal(t, 2026, a.got.From.Year())

	rec = do(t, h, http.MethodGet, "/api/v1/charts?from=yesterday", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = do(t, h, http.MethodPost, "/api/v1/charts", `{}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var body errorBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Contains(t, body.Error, "items or categories")
}

func TestRoutes(t *testing.T) {
	h := NewHandler(&fakeCatalog{}, &fakeLedger{}, &fakeAnalytics{}, nil, zap.NewNop()).(*gin.Engine)

	var got []string
	for _, r := range h.Routes() {
		got = append(got, r.Method+" "+r.Path)
	}
	assert.ElementsMatch(t, []string{
		"GET /healthz",
		"GET /readyz",
		"GET /api/v1/items/lookup",
		"GET /api/v1/items/:id/codes",
		"GET /api/v1/items/:id/events",
		"POST /api/v1/items/:id/events",
		"POST /api/v1/items/:id/reconcile",
		"POST /api/v1/codes",
		"DELETE /api/v1/codes/:id",
		"POST /api/v1/scan",
		"POST /api/v1/reconcile",
		"GET /api/v1/reconciliations",
		"GET /api/v1/charts",
		"POST /api/v1/charts",
		"POST /api/v1/sync",
	}, got)
}
