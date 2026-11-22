package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/homebox"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/analytics"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/catalog"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/ledger"
)

type fakeCatalog struct {
	result  catalog.LookupResult
	linked  []string
	deleted []string
}

func (f *fakeCatalog) Resolve(context.Context, catalog.LookupQuery) (catalog.LookupResult, error) {
	return f.result, nil
}

func (f *fakeCatalog) LinkCode(_ context.Context, code, vendor, itemID string, src catalog.CodeSource) (*catalog.CodeLink, error) {
	f.linked = append(f.linked, code+"@"+vendor+"->"+itemID+":"+string(src))
	return &catalog.CodeLink{ID: "l1", Code: code, ItemID: itemID, Source: src}, nil
}

func (f *fakeCatalog) ListCodes(context.Context, string) ([]catalog.CodeLink, error) { return nil, nil }

func (f *fakeCatalog) DeleteCode(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeCatalog) Sync(context.Context) (catalog.SyncStats, error) {
	return catalog.SyncStats{Seen: 3}, nil
}

type fakeLedger struct {
	events    []ledger.EventInput
	reconcile []ledger.ReconcileInput
	filter    ledger.EventFilter
	err       error
}

func (f *fakeLedger) RecordEvent(_ context.Context, in ledger.EventInput) (*ledger.Event, error) {
	if _, err := in.Delta(); err != nil {
		return nil, err
	}
	if f.err != nil {
		return nil, f.err
	}
	f.events = append(f.events, in)
	return &ledger.Event{ID: "e1", ItemID: in.ItemID, Type: in.Type, Delta: in.Quantity}, nil
}

func (f *fakeLedger) ReconcileBatch(_ context.Context, in []ledger.ReconcileInput) []ledger.ReconcileResult {
	f.reconcile = in
	out := make([]ledger.ReconcileResult, len(in))
	for i, r := range in {
		out[i] = ledger.ReconcileResult{ItemID: r.ItemID, Reconciliation: &ledger.Reconciliation{ItemID: r.ItemID, CountedQty: r.Counted, Applied: r.Apply}}
	}
	return out
}

func (f *fakeLedger) ListEvents(_ context.Context, filter ledger.EventFilter) ([]ledger.Event, error) {
	f.filter = filter
	return nil, nil
}

func (f *fakeLedger) ListReconciliations(context.Context, string, int) ([]ledger.Reconciliation, error) {
	return nil, nil
}

type fakeAnalytics struct{ got analytics.Query }

func (f *fakeAnalytics) Series(_ context.Context, q analytics.Query) (*analytics.Chart, error) {
	f.got = q
	if len(q.Items) == 0 && len(q.Categories) == 0 {
		return nil, &analytics.ValidationError{Msg: "at least one of items or categories is required"}
	}
	return &analytics.Chart{Metric: q.Metric, Series: []analytics.Series{}}, nil
}

type fakeHomebox struct{ opts homebox.ListOptions }

func (f *fakeHomebox) ListEntities(_ context.Context, opts homebox.ListOptions) (*homebox.EntityPage, error) {
	f.opts = opts
	return &homebox.EntityPage{Items: []homebox.EntitySummary{{ID: "rice", Name: "Rice", Quantity: 2, Tags: []homebox.Tag{{ID: "t1", Name: "Pantry"}}}}, Total: 1}, nil
}

func (f *fakeHomebox) GetEntity(_ context.Context, id string) (*homebox.Entity, error) {
	if id != "rice" {
		return nil, homebox.ErrNotFound
	}
	return &homebox.Entity{EntitySummary: homebox.EntitySummary{ID: "rice", Name: "Rice"}}, nil
}

func (f *fakeHomebox) ListTags(context.Context) ([]homebox.Tag, error) {
	return []homebox.Tag{{ID: "t1", Name: "Pantry"}}, nil
}

type fixture struct {
	catalog   *fakeCatalog
	ledger    *fakeLedger
	analytics *fakeAnalytics
	homebox   *fakeHomebox
	session   *sdk.ClientSession
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		catalog: &fakeCatalog{result: catalog.LookupResult{
			MatchType: catalog.MatchHybrid, Confident: true, Candidates: []catalog.Candidate{{ItemID: "rice", Name: "Rice"}},
		}},
		ledger:    &fakeLedger{},
		analytics: &fakeAnalytics{},
		homebox:   &fakeHomebox{},
	}

	srv, err := NewServer(Config{Version: "test", CodeFields: []string{"EAN", "SKU"}},
		Deps{Catalog: f.catalog, Ledger: f.ledger, Analytics: f.analytics, Homebox: f.homebox}, zap.NewNop())
	require.NoError(t, err)

	ctx := context.Background()
	serverT, clientT := sdk.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, serverT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })

	f.session, err = sdk.NewClient(&sdk.Implementation{Name: "test"}, nil).Connect(ctx, clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.session.Close() })
	return f
}

// call invokes a tool and decodes its structured output into out (when non-nil).
func (f *fixture) call(t *testing.T, name string, args map[string]any, out any) *sdk.CallToolResult {
	t.Helper()
	res, err := f.session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	if out != nil && !res.IsError {
		b, err := json.Marshal(res.StructuredContent)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(b, out))
	}
	return res
}

func errorText(res *sdk.CallToolResult) string {
	if len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(*sdk.TextContent); ok {
		return tc.Text
	}
	return ""
}

func TestServerInfo(t *testing.T) {
	f := newFixture(t)

	init := f.session.InitializeResult()
	assert.Contains(t, init.Instructions, "EAN, SKU")

	tools, err := f.session.ListTools(context.Background(), nil)
	require.NoError(t, err)

	names := map[string]*sdk.Tool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = tool
		assert.NotEmpty(t, tool.Description, tool.Name)
		assert.NotNil(t, tool.InputSchema, tool.Name)
		assert.NotNil(t, tool.Annotations, tool.Name)
	}
	assert.Len(t, names, 14)

	assert.True(t, names["search_items"].Annotations.ReadOnlyHint)
	assert.True(t, *names["delete_code"].Annotations.DestructiveHint)
	assert.Contains(t, names["record_event"].Description, "purchase, consume, adjust")

	schema, err := json.Marshal(names["record_event"].InputSchema)
	require.NoError(t, err)
	assert.Contains(t, string(schema), `"enum":["purchase","consume","adjust"]`)
}

func TestBrowsing(t *testing.T) {
	f := newFixture(t)

	var page searchItemsOutput
	res := f.call(t, "search_items", map[string]any{"query": "rice", "page_size": 500}, &page)
	require.False(t, res.IsError, errorText(res))
	assert.Equal(t, maxPageSize, f.homebox.opts.PageSize)
	assert.Equal(t, 1, f.homebox.opts.Page)
	require.Len(t, page.Items, 1)
	assert.Equal(t, []string{"Pantry"}, page.Items[0].Tags)

	var item getItemOutput
	res = f.call(t, "get_item", map[string]any{"item_id": "rice"}, &item)
	require.False(t, res.IsError, errorText(res))
	assert.Equal(t, "Rice", item.Item.Name)
	assert.NotNil(t, item.Codes)

	res = f.call(t, "get_item", map[string]any{"item_id": "nope"}, nil)
	assert.True(t, res.IsError)
	assert.Contains(t, errorText(res), "not found")

	res = f.call(t, "list_tags", nil, nil)
	assert.False(t, res.IsError, errorText(res))
}

func TestRecordEvent(t *testing.T) {
	f := newFixture(t)

	var ev ledger.Event
	res := f.call(t, "record_event", map[string]any{"item_id": "rice", "type": "purchase", "quantity": 2, "unit_price": 1.5, "occurred_at": "2026-11-01T10:00:00Z"}, &ev)
	require.False(t, res.IsError, errorText(res))
	require.Len(t, f.ledger.events, 1)
	assert.Equal(t, "rice", f.ledger.events[0].ItemID)
	assert.Equal(t, 1.5, *f.ledger.events[0].UnitPrice)
	assert.Equal(t, 2026, f.ledger.events[0].OccurredAt.Year())

	// Rejected by the schema enum before reaching the ledger.
	res = f.call(t, "record_event", map[string]any{"item_id": "rice", "type": "steal", "quantity": 1}, nil)
	assert.True(t, res.IsError)

	// Domain validation is passed through.
	res = f.call(t, "record_event", map[string]any{"item_id": "rice", "type": "consume", "quantity": -1}, nil)
	assert.True(t, res.IsError)
	assert.Contains(t, errorText(res), "must be positive")

	f.ledger.err = ledger.ErrInsufficientQuantity
	res = f.call(t, "record_event", map[string]any{"item_id": "rice", "type": "consume", "quantity": 1}, nil)
	assert.True(t, res.IsError)
	assert.Contains(t, errorText(res), "insufficient quantity")

	f.ledger.err = errors.New("db exploded")
	res = f.call(t, "record_event", map[string]any{"item_id": "rice", "type": "consume", "quantity": 1}, nil)
	assert.True(t, res.IsError)
	assert.Equal(t, "internal error", errorText(res))
}

func TestScan(t *testing.T) {
	t.Run("confident match records and learns", func(t *testing.T) {
		f := newFixture(t)
		var out scanOutput
		res := f.call(t, "scan", map[string]any{"code": "V-1", "vendor": "shop", "text": "RICE 1KG", "learn": true, "type": "purchase", "quantity": 1}, &out)
		require.False(t, res.IsError, errorText(res))
		assert.True(t, out.Recorded)
		require.Len(t, f.ledger.events, 1)
		assert.Equal(t, "V-1", f.ledger.events[0].CodeUsed)
		assert.Equal(t, []string{"V-1@shop->rice:learned"}, f.catalog.linked)
	})

	t.Run("no confident match returns candidates", func(t *testing.T) {
		f := newFixture(t)
		f.catalog.result.Confident = false
		var out scanOutput
		res := f.call(t, "scan", map[string]any{"text": "rice", "type": "consume", "quantity": 1}, &out)
		require.False(t, res.IsError, errorText(res))
		assert.False(t, out.Recorded)
		assert.Len(t, out.Match.Candidates, 1)
		assert.Empty(t, f.ledger.events)
	})
}

func TestReconcileAndCodes(t *testing.T) {
	f := newFixture(t)

	var rec reconcileOutput
	res := f.call(t, "reconcile", map[string]any{"items": []map[string]any{{"item_id": "rice", "counted": 4}}}, &rec)
	require.False(t, res.IsError, errorText(res))
	require.Len(t, f.ledger.reconcile, 1)
	assert.False(t, f.ledger.reconcile[0].Apply)
	assert.Len(t, rec.Results, 1)

	res = f.call(t, "reconcile", map[string]any{"items": []map[string]any{}}, nil)
	assert.True(t, res.IsError)

	res = f.call(t, "link_code", map[string]any{"code": "123", "item_id": "rice"}, nil)
	require.False(t, res.IsError, errorText(res))
	assert.Equal(t, []string{"123@->rice:manual"}, f.catalog.linked)

	res = f.call(t, "delete_code", map[string]any{"code_id": "l1"}, nil)
	require.False(t, res.IsError, errorText(res))
	assert.Equal(t, []string{"l1"}, f.catalog.deleted)

	for _, name := range []string{"list_codes", "list_events"} {
		res = f.call(t, name, map[string]any{"item_id": "rice"}, nil)
		assert.False(t, res.IsError, name+": "+errorText(res))
	}
	for _, name := range []string{"list_reconciliations", "sync_catalog", "lookup_item"} {
		res = f.call(t, name, map[string]any{}, nil)
		assert.False(t, res.IsError, name+": "+errorText(res))
	}
}

func TestGetChart(t *testing.T) {
	f := newFixture(t)

	res := f.call(t, "get_chart", map[string]any{"categories": []string{"all"}, "metric": "spend", "interval": "month", "from": "2026-01-01T00:00:00Z"}, nil)
	require.False(t, res.IsError, errorText(res))
	assert.Equal(t, []string{"all"}, f.analytics.got.Categories)
	assert.Equal(t, analytics.MetricSpend, f.analytics.got.Metric)
	assert.Equal(t, analytics.IntervalMonth, f.analytics.got.Interval)
	assert.Equal(t, 2026, f.analytics.got.From.Year())

	res = f.call(t, "get_chart", map[string]any{}, nil)
	assert.True(t, res.IsError)
	assert.Contains(t, errorText(res), "items or categories")
}

func TestStreamableHTTP(t *testing.T) {
	h, err := NewHandler(Config{Version: "test"}, Deps{Catalog: &fakeCatalog{}, Ledger: &fakeLedger{}, Analytics: &fakeAnalytics{}, Homebox: &fakeHomebox{}}, zap.NewNop())
	require.NoError(t, err)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	ctx := context.Background()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "test"}, nil).Connect(ctx, &sdk.StreamableClientTransport{Endpoint: srv.URL}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	res, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "list_tags"})
	require.NoError(t, err)
	assert.False(t, res.IsError, errorText(res))
}
