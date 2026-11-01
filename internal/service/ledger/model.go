package ledger

import (
	"errors"
	"time"
)

type EventType string

const (
	EventPurchase  EventType = "purchase"
	EventConsume   EventType = "consume"
	EventAdjust    EventType = "adjust"
	EventReconcile EventType = "reconcile"
)

var (
	ErrNotFound             = errors.New("not found")
	ErrInsufficientQuantity = errors.New("insufficient quantity")
)

// ValidationError marks invalid input.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

type Event struct {
	ID            string    `json:"id"`
	ItemID        string    `json:"item_id"`
	Type          EventType `json:"type"`
	Delta         float64   `json:"delta"`
	QuantityAfter float64   `json:"quantity_after"`
	UnitPrice     *float64  `json:"unit_price,omitempty"`
	Currency      string    `json:"currency,omitempty"`
	Vendor        string    `json:"vendor,omitempty"`
	CodeUsed      string    `json:"code_used,omitempty"`
	Note          string    `json:"note,omitempty"`
	OccurredAt    time.Time `json:"occurred_at"`
	CreatedAt     time.Time `json:"created_at"`
}

// EventInput records a purchase/consume (Quantity > 0) or an adjust (signed Quantity).
type EventInput struct {
	ItemID     string    `json:"-"`
	Type       EventType `json:"type"`
	Quantity   float64   `json:"quantity"`
	UnitPrice  *float64  `json:"unit_price,omitempty"`
	Currency   string    `json:"currency,omitempty"`
	Vendor     string    `json:"vendor,omitempty"`
	CodeUsed   string    `json:"code_used,omitempty"`
	Note       string    `json:"note,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
	// AllowNegative lets consumption drive the Homebox quantity below zero.
	AllowNegative bool `json:"allow_negative,omitempty"`
}

// Delta returns the signed quantity change for the input.
func (in EventInput) Delta() (float64, error) {
	switch in.Type {
	case EventPurchase:
		if in.Quantity <= 0 {
			return 0, &ValidationError{"purchase quantity must be positive"}
		}
		return in.Quantity, nil
	case EventConsume:
		if in.Quantity <= 0 {
			return 0, &ValidationError{"consume quantity must be positive"}
		}
		return -in.Quantity, nil
	case EventAdjust:
		if in.Quantity == 0 {
			return 0, &ValidationError{"adjust quantity must be non-zero"}
		}
		return in.Quantity, nil
	default:
		return 0, &ValidationError{"type must be one of purchase, consume, adjust"}
	}
}

type Reconciliation struct {
	ID         string    `json:"id"`
	ItemID     string    `json:"item_id"`
	HomeboxQty float64   `json:"homebox_qty"`
	CountedQty float64   `json:"counted_qty"`
	Delta      float64   `json:"delta"`
	Applied    bool      `json:"applied"`
	Note       string    `json:"note,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	Event      *Event    `json:"event,omitempty"`
}

type ReconcileInput struct {
	ItemID  string  `json:"item_id"`
	Counted float64 `json:"counted"`
	// Apply pushes the counted quantity to Homebox and records a reconcile event.
	Apply bool   `json:"apply"`
	Note  string `json:"note,omitempty"`
}

type ReconcileResult struct {
	ItemID         string          `json:"item_id"`
	Reconciliation *Reconciliation `json:"reconciliation,omitempty"`
	Error          string          `json:"error,omitempty"`
}

type EventFilter struct {
	ItemID string
	From   time.Time
	To     time.Time
	Limit  int
}
