// Package mcp exposes inventory management (item lookup, stock events, stock-takes, code links and
// charts) as a Model Context Protocol server over Streamable HTTP.
package mcp

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"net/http"
	"strings"
	"text/template"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"

	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/homebox"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/analytics"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/catalog"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/ledger"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/scan"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

type Catalog interface {
	Resolve(ctx context.Context, q catalog.LookupQuery) (catalog.LookupResult, error)
	LinkCode(ctx context.Context, code, vendor, itemID string, source catalog.CodeSource) (*catalog.CodeLink, error)
	ListCodes(ctx context.Context, itemID string) ([]catalog.CodeLink, error)
	DeleteCode(ctx context.Context, id string) error
	Sync(ctx context.Context) (catalog.SyncStats, error)
}

type Ledger interface {
	RecordEvent(ctx context.Context, in ledger.EventInput) (*ledger.Event, error)
	ReconcileBatch(ctx context.Context, in []ledger.ReconcileInput) []ledger.ReconcileResult
	ListEvents(ctx context.Context, f ledger.EventFilter) ([]ledger.Event, error)
	ListReconciliations(ctx context.Context, itemID string, limit int) ([]ledger.Reconciliation, error)
}

type Analytics interface {
	Series(ctx context.Context, q analytics.Query) (*analytics.Chart, error)
}

// Homebox is the read-only part of the Homebox client used for browsing.
type Homebox interface {
	ListEntities(ctx context.Context, opts homebox.ListOptions) (*homebox.EntityPage, error)
	GetEntity(ctx context.Context, id string) (*homebox.Entity, error)
	ListTags(ctx context.Context) ([]homebox.Tag, error)
}

type Config struct {
	Version string
	// CodeFields are the Homebox custom fields treated as product codes (mentioned to the model).
	CodeFields []string
}

type Deps struct {
	Catalog   Catalog
	Ledger    Ledger
	Analytics Analytics
	Homebox   Homebox
}

type server struct {
	deps    Deps
	scanner *scan.Service
	logger  *zap.Logger
}

// templateData is available to the instruction and tool description templates.
type templateData struct {
	CodeFields []string
	EventTypes []string
	Metrics    []string
	Intervals  []string
	GroupBys   []string
}

// NewServer builds the MCP server with all inventory tools registered.
func NewServer(cfg Config, deps Deps, logger *zap.Logger) (*sdk.Server, error) {
	texts, err := renderTemplates(templateData{
		CodeFields: cfg.CodeFields,
		EventTypes: strs(ledger.EventPurchase, ledger.EventConsume, ledger.EventAdjust),
		Metrics:    strs(analytics.MetricConsumed, analytics.MetricPurchased, analytics.MetricSpend, analytics.MetricQuantity, analytics.MetricEvents),
		Intervals:  strs(analytics.IntervalDay, analytics.IntervalWeek, analytics.IntervalMonth, analytics.IntervalQuarter, analytics.IntervalYear),
		GroupBys:   strs(analytics.GroupByItem, analytics.GroupByCategory, analytics.GroupByTotal),
	})
	if err != nil {
		return nil, err
	}

	srv := sdk.NewServer(&sdk.Implementation{Name: "homebox-analytics", Version: cfg.Version}, &sdk.ServerOptions{
		Instructions: texts["instructions"],
	})
	srv.AddReceivingMiddleware(tracing())

	s := &server{deps: deps, scanner: scan.NewService(deps.Catalog, deps.Ledger), logger: logger}
	if err := s.register(srv, texts); err != nil {
		return nil, err
	}
	return srv, nil
}

// NewHandler serves the MCP server over stateless Streamable HTTP.
func NewHandler(cfg Config, deps Deps, logger *zap.Logger) (http.Handler, error) {
	srv, err := NewServer(cfg, deps, logger)
	if err != nil {
		return nil, err
	}
	return sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv }, &sdk.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
	}), nil
}

// renderTemplates renders every embedded template, keyed by file name without extension.
func renderTemplates(data templateData) (map[string]string, error) {
	tmpl, err := template.New("").Funcs(template.FuncMap{"join": strings.Join}).ParseFS(templateFS, "templates/*.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parse mcp templates: %w", err)
	}

	out := map[string]string{}
	for _, t := range tmpl.Templates() {
		if t.Name() == "" {
			continue
		}
		var buf bytes.Buffer
		if err := t.Execute(&buf, data); err != nil {
			return nil, fmt.Errorf("render mcp template %q: %w", t.Name(), err)
		}
		out[strings.TrimSuffix(t.Name(), ".tmpl")] = strings.TrimSpace(buf.String())
	}
	return out, nil
}

func strs[T ~string](vs ...T) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = string(v)
	}
	return out
}
