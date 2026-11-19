package mcp

import (
	"time"

	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/homebox"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/catalog"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/ledger"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/scan"
)

// Tool inputs and outputs. Field tags define the JSON schemas advertised to MCP clients: fields
// without omitempty are required, and the jsonschema tag is the property description.

// --- Homebox browsing ---

type searchItemsInput struct {
	Query    string   `json:"query,omitempty" jsonschema:"name or keyword to search for; empty lists all items"`
	TagIDs   []string `json:"tag_ids,omitempty" jsonschema:"only items with any of these tag IDs"`
	Page     int      `json:"page,omitempty" jsonschema:"1-based page number, default 1"`
	PageSize int      `json:"page_size,omitempty" jsonschema:"items per page, default 25, max 100"`
}

type itemSummary struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Quantity float64  `json:"quantity"`
	Tags     []string `json:"tags"`
	Archived bool     `json:"archived,omitempty"`
}

type searchItemsOutput struct {
	Items    []itemSummary `json:"items"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
	Total    int           `json:"total"`
}

type itemIDInput struct {
	ItemID string `json:"item_id" jsonschema:"Homebox item ID"`
}

type getItemOutput struct {
	Item  *homebox.Entity    `json:"item"`
	Codes []catalog.CodeLink `json:"codes"`
}

type emptyInput struct{}

type listTagsOutput struct {
	Tags []homebox.Tag `json:"tags"`
}

// --- Matching ---

type lookupItemInput struct {
	Code   string `json:"code,omitempty" jsonschema:"barcode or product code"`
	Vendor string `json:"vendor,omitempty" jsonschema:"store the code comes from, for store-specific codes"`
	Text   string `json:"text,omitempty" jsonschema:"item name or receipt line"`
	Limit  int    `json:"limit,omitempty" jsonschema:"maximum number of candidates, default 5"`
}

// --- Ledger ---

// eventFields are shared by record_event and scan.
type eventFields struct {
	Type          string     `json:"type" jsonschema:"purchase, consume or adjust"`
	Quantity      float64    `json:"quantity" jsonschema:"amount; positive for purchase and consume, signed for adjust"`
	UnitPrice     *float64   `json:"unit_price,omitempty" jsonschema:"price per unit, for purchases"`
	Currency      string     `json:"currency,omitempty" jsonschema:"ISO 4217 currency code of unit_price"`
	Vendor        string     `json:"vendor,omitempty" jsonschema:"store where it was bought"`
	Note          string     `json:"note,omitempty" jsonschema:"free-form note"`
	OccurredAt    *time.Time `json:"occurred_at,omitempty" jsonschema:"when it happened (RFC 3339), default now"`
	AllowNegative bool       `json:"allow_negative,omitempty" jsonschema:"allow consumption to drive the quantity below zero"`
}

func (f eventFields) input(itemID, code string) ledger.EventInput {
	in := ledger.EventInput{
		ItemID:        itemID,
		Type:          ledger.EventType(f.Type),
		Quantity:      f.Quantity,
		UnitPrice:     f.UnitPrice,
		Currency:      f.Currency,
		Vendor:        f.Vendor,
		CodeUsed:      code,
		Note:          f.Note,
		AllowNegative: f.AllowNegative,
	}
	if f.OccurredAt != nil {
		in.OccurredAt = *f.OccurredAt
	}
	return in
}

type recordEventInput struct {
	ItemID string `json:"item_id" jsonschema:"Homebox item ID"`
	eventFields
}

type scanInput struct {
	Code  string `json:"code,omitempty" jsonschema:"barcode or product code"`
	Text  string `json:"text,omitempty" jsonschema:"item name or receipt line"`
	Learn bool   `json:"learn,omitempty" jsonschema:"link the code to the item when it was matched by text"`
	eventFields
}

type scanOutput struct {
	// Recorded is false when there was no confident match and nothing was changed.
	Recorded bool `json:"recorded"`
	scan.Result
}

type listEventsInput struct {
	ItemID string     `json:"item_id" jsonschema:"Homebox item ID"`
	From   *time.Time `json:"from,omitempty" jsonschema:"only events at or after this time (RFC 3339)"`
	To     *time.Time `json:"to,omitempty" jsonschema:"only events before this time (RFC 3339)"`
	Limit  int        `json:"limit,omitempty" jsonschema:"maximum number of events, default 100"`
}

type listEventsOutput struct {
	Events []ledger.Event `json:"events"`
}

type reconcileItem struct {
	ItemID  string  `json:"item_id" jsonschema:"Homebox item ID"`
	Counted float64 `json:"counted" jsonschema:"physically counted quantity"`
	Note    string  `json:"note,omitempty" jsonschema:"free-form note"`
}

type reconcileInput struct {
	Items []reconcileItem `json:"items" jsonschema:"counted items"`
	Apply bool            `json:"apply,omitempty" jsonschema:"write counted quantities to Homebox; false only previews the differences"`
}

type reconcileOutput struct {
	Results []ledger.ReconcileResult `json:"results"`
}

type listReconciliationsInput struct {
	ItemID string `json:"item_id,omitempty" jsonschema:"only stock-takes of this item"`
	Limit  int    `json:"limit,omitempty" jsonschema:"maximum number of results"`
}

type listReconciliationsOutput struct {
	Reconciliations []ledger.Reconciliation `json:"reconciliations"`
}

// --- Code links ---

type linkCodeInput struct {
	Code   string `json:"code" jsonschema:"barcode or product code"`
	Vendor string `json:"vendor,omitempty" jsonschema:"store the code belongs to; empty for universal codes like EAN"`
	ItemID string `json:"item_id" jsonschema:"Homebox item ID"`
}

type listCodesOutput struct {
	Codes []catalog.CodeLink `json:"codes"`
}

type deleteCodeInput struct {
	CodeID string `json:"code_id" jsonschema:"ID of the code link"`
}

type deleteCodeOutput struct {
	Deleted bool `json:"deleted"`
}

// --- Analytics ---

type getChartInput struct {
	Items      []string   `json:"items,omitempty" jsonschema:"item IDs to include"`
	Categories []string   `json:"categories,omitempty" jsonschema:"tag IDs to include, or [\"all\"] for every tag"`
	GroupBy    string     `json:"group_by,omitempty" jsonschema:"how to split series, default item (category when categories is [\"all\"])"`
	Metric     string     `json:"metric,omitempty" jsonschema:"what to aggregate, default consumed"`
	Interval   string     `json:"interval,omitempty" jsonschema:"bucket size, default week"`
	From       *time.Time `json:"from,omitempty" jsonschema:"start of the range (RFC 3339), default one year before to"`
	To         *time.Time `json:"to,omitempty" jsonschema:"end of the range (RFC 3339), default now"`
	Timezone   string     `json:"timezone,omitempty" jsonschema:"IANA time zone for bucket boundaries, default UTC"`
}
