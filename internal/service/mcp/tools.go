package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/homebox"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/analytics"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/catalog"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/ledger"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/scan"
)

const (
	defaultPageSize = 25
	maxPageSize     = 100
)

type toolKind int

const (
	readOnly toolKind = iota
	write
	destructive
)

type toolSpec struct {
	name  string
	title string
	kind  toolKind
	// enums restricts top-level string properties to the given values.
	enums map[string][]string
}

func (s *server) register(srv *sdk.Server, texts map[string]string) error {
	eventTypes := strs(ledger.EventPurchase, ledger.EventConsume, ledger.EventAdjust)
	regs := []func() error{
		func() error {
			return addTool(s, srv, texts, toolSpec{name: "search_items", title: "Search items"}, s.searchItems)
		},
		func() error {
			return addTool(s, srv, texts, toolSpec{name: "get_item", title: "Get item"}, s.getItem)
		},
		func() error {
			return addTool(s, srv, texts, toolSpec{name: "list_tags", title: "List tags"}, s.listTags)
		},
		func() error {
			return addTool(s, srv, texts, toolSpec{name: "lookup_item", title: "Look up item by code or text"}, s.lookupItem)
		},
		func() error {
			return addTool(s, srv, texts, toolSpec{name: "record_event", title: "Record stock event", kind: write,
				enums: map[string][]string{"type": eventTypes}}, s.recordEvent)
		},
		func() error {
			return addTool(s, srv, texts, toolSpec{name: "scan", title: "Scan and record", kind: write,
				enums: map[string][]string{"type": eventTypes}}, s.scan)
		},
		func() error {
			return addTool(s, srv, texts, toolSpec{name: "list_events", title: "List stock events"}, s.listEvents)
		},
		func() error {
			return addTool(s, srv, texts, toolSpec{name: "reconcile", title: "Stock-take", kind: destructive}, s.reconcile)
		},
		func() error {
			return addTool(s, srv, texts, toolSpec{name: "list_reconciliations", title: "List stock-takes"}, s.listReconciliations)
		},
		func() error {
			return addTool(s, srv, texts, toolSpec{name: "link_code", title: "Link product code", kind: write}, s.linkCode)
		},
		func() error {
			return addTool(s, srv, texts, toolSpec{name: "list_codes", title: "List product codes"}, s.listCodes)
		},
		func() error {
			return addTool(s, srv, texts, toolSpec{name: "delete_code", title: "Delete product code link", kind: destructive}, s.deleteCode)
		},
		func() error {
			return addTool(s, srv, texts, toolSpec{name: "get_chart", title: "Usage and spend chart", enums: map[string][]string{
				"metric":   strs(analytics.MetricConsumed, analytics.MetricPurchased, analytics.MetricSpend, analytics.MetricQuantity, analytics.MetricEvents),
				"interval": strs(analytics.IntervalDay, analytics.IntervalWeek, analytics.IntervalMonth, analytics.IntervalQuarter, analytics.IntervalYear),
				"group_by": strs(analytics.GroupByItem, analytics.GroupByCategory, analytics.GroupByTotal),
			}}, s.getChart)
		},
		func() error {
			return addTool(s, srv, texts, toolSpec{name: "sync_catalog", title: "Re-index items", kind: write}, s.syncCatalog)
		},
	}
	for _, reg := range regs {
		if err := reg(); err != nil {
			return err
		}
	}
	return nil
}

// addTool registers a typed tool whose description comes from the template of the same name.
func addTool[In, Out any](s *server, srv *sdk.Server, texts map[string]string, spec toolSpec, h func(context.Context, In) (Out, error)) error {
	desc, ok := texts[spec.name]
	if !ok {
		return fmt.Errorf("mcp: no description template for tool %q", spec.name)
	}

	schema, err := jsonschema.For[In](nil)
	if err != nil {
		return fmt.Errorf("mcp: input schema for %q: %w", spec.name, err)
	}
	for prop, values := range spec.enums {
		p, ok := schema.Properties[prop]
		if !ok {
			return fmt.Errorf("mcp: tool %q has no property %q", spec.name, prop)
		}
		for _, v := range values {
			p.Enum = append(p.Enum, v)
		}
	}

	no := false
	ann := &sdk.ToolAnnotations{Title: spec.title, OpenWorldHint: &no}
	switch spec.kind {
	case readOnly:
		ann.ReadOnlyHint = true
	case write:
		ann.DestructiveHint = &no
	case destructive:
		yes := true
		ann.DestructiveHint = &yes
	}

	sdk.AddTool(srv, &sdk.Tool{Name: spec.name, Title: spec.title, Description: desc, InputSchema: schema, Annotations: ann},
		func(ctx context.Context, _ *sdk.CallToolRequest, in In) (*sdk.CallToolResult, Out, error) {
			out, err := h(ctx, in)
			if err != nil {
				var zero Out
				return nil, zero, s.toolError(spec.name, err)
			}
			return nil, out, nil
		})
	return nil
}

func nonNil[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

// --- Homebox browsing ---

func (s *server) searchItems(ctx context.Context, in searchItemsInput) (searchItemsOutput, error) {
	if in.Page <= 0 {
		in.Page = 1
	}
	if in.PageSize <= 0 {
		in.PageSize = defaultPageSize
	}
	in.PageSize = min(in.PageSize, maxPageSize)

	page, err := s.deps.Homebox.ListEntities(ctx, homebox.ListOptions{Query: in.Query, TagIDs: in.TagIDs, Page: in.Page, PageSize: in.PageSize})
	if err != nil {
		return searchItemsOutput{}, err
	}

	out := searchItemsOutput{Items: make([]itemSummary, len(page.Items)), Page: in.Page, PageSize: in.PageSize, Total: page.Total}
	for i, e := range page.Items {
		tags := make([]string, len(e.Tags))
		for j, t := range e.Tags {
			tags[j] = t.Name
		}
		out.Items[i] = itemSummary{ID: e.ID, Name: e.Name, Quantity: e.Quantity, Tags: tags, Archived: e.Archived}
	}
	return out, nil
}

func (s *server) getItem(ctx context.Context, in itemIDInput) (getItemOutput, error) {
	item, err := s.deps.Homebox.GetEntity(ctx, in.ItemID)
	if err != nil {
		return getItemOutput{}, err
	}
	codes, err := s.deps.Catalog.ListCodes(ctx, in.ItemID)
	if err != nil {
		return getItemOutput{}, err
	}
	return getItemOutput{Item: item, Codes: nonNil(codes)}, nil
}

func (s *server) listTags(ctx context.Context, _ emptyInput) (listTagsOutput, error) {
	tags, err := s.deps.Homebox.ListTags(ctx)
	if err != nil {
		return listTagsOutput{}, err
	}
	return listTagsOutput{Tags: nonNil(tags)}, nil
}

// --- Matching ---

func (s *server) lookupItem(ctx context.Context, in lookupItemInput) (catalog.LookupResult, error) {
	res, err := s.deps.Catalog.Resolve(ctx, catalog.LookupQuery{Code: in.Code, Vendor: in.Vendor, Text: in.Text, Limit: in.Limit})
	res.Candidates = nonNil(res.Candidates)
	return res, err
}

// --- Ledger ---

func (s *server) recordEvent(ctx context.Context, in recordEventInput) (*ledger.Event, error) {
	return s.deps.Ledger.RecordEvent(ctx, in.input(in.ItemID, ""))
}

func (s *server) scan(ctx context.Context, in scanInput) (scanOutput, error) {
	res, err := s.scanner.Scan(ctx, scan.Request{
		Code: in.Code, Vendor: in.Vendor, Text: in.Text, Learn: in.Learn, Event: in.input("", in.Code),
	})
	res.Match.Candidates = nonNil(res.Match.Candidates)
	if errors.Is(err, scan.ErrNoConfidentMatch) {
		return scanOutput{Result: res}, nil
	}
	if err != nil {
		return scanOutput{}, err
	}
	return scanOutput{Recorded: true, Result: res}, nil
}

func (s *server) listEvents(ctx context.Context, in listEventsInput) (listEventsOutput, error) {
	f := ledger.EventFilter{ItemID: in.ItemID, Limit: in.Limit}
	if in.From != nil {
		f.From = *in.From
	}
	if in.To != nil {
		f.To = *in.To
	}
	events, err := s.deps.Ledger.ListEvents(ctx, f)
	if err != nil {
		return listEventsOutput{}, err
	}
	return listEventsOutput{Events: nonNil(events)}, nil
}

func (s *server) reconcile(ctx context.Context, in reconcileInput) (reconcileOutput, error) {
	if len(in.Items) == 0 {
		return reconcileOutput{}, &inputError{"items must not be empty"}
	}
	inputs := make([]ledger.ReconcileInput, len(in.Items))
	for i, it := range in.Items {
		inputs[i] = ledger.ReconcileInput{ItemID: it.ItemID, Counted: it.Counted, Apply: in.Apply, Note: it.Note}
	}
	return reconcileOutput{Results: nonNil(s.deps.Ledger.ReconcileBatch(ctx, inputs))}, nil
}

func (s *server) listReconciliations(ctx context.Context, in listReconciliationsInput) (listReconciliationsOutput, error) {
	recs, err := s.deps.Ledger.ListReconciliations(ctx, in.ItemID, in.Limit)
	if err != nil {
		return listReconciliationsOutput{}, err
	}
	return listReconciliationsOutput{Reconciliations: nonNil(recs)}, nil
}

// --- Code links ---

func (s *server) linkCode(ctx context.Context, in linkCodeInput) (*catalog.CodeLink, error) {
	return s.deps.Catalog.LinkCode(ctx, in.Code, in.Vendor, in.ItemID, catalog.SourceManual)
}

func (s *server) listCodes(ctx context.Context, in itemIDInput) (listCodesOutput, error) {
	codes, err := s.deps.Catalog.ListCodes(ctx, in.ItemID)
	if err != nil {
		return listCodesOutput{}, err
	}
	return listCodesOutput{Codes: nonNil(codes)}, nil
}

func (s *server) deleteCode(ctx context.Context, in deleteCodeInput) (deleteCodeOutput, error) {
	if err := s.deps.Catalog.DeleteCode(ctx, in.CodeID); err != nil {
		return deleteCodeOutput{}, err
	}
	return deleteCodeOutput{Deleted: true}, nil
}

// --- Analytics ---

func (s *server) getChart(ctx context.Context, in getChartInput) (*analytics.Chart, error) {
	q := analytics.Query{
		Items:      in.Items,
		Categories: in.Categories,
		GroupBy:    analytics.GroupBy(in.GroupBy),
		Metric:     analytics.Metric(in.Metric),
		Interval:   analytics.Interval(in.Interval),
		Timezone:   in.Timezone,
	}
	if in.From != nil {
		q.From = *in.From
	}
	if in.To != nil {
		q.To = *in.To
	}
	return s.deps.Analytics.Series(ctx, q)
}

// --- Sync ---

func (s *server) syncCatalog(ctx context.Context, _ emptyInput) (catalog.SyncStats, error) {
	return s.deps.Catalog.Sync(ctx)
}
