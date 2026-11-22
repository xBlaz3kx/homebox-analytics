package scan

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xBlaz3kx/homebox-analytics/internal/service/catalog"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/ledger"
)

type fakeCatalog struct {
	result catalog.LookupResult
	err    error
	linked []string
}

func (f *fakeCatalog) Resolve(context.Context, catalog.LookupQuery) (catalog.LookupResult, error) {
	return f.result, f.err
}

func (f *fakeCatalog) LinkCode(_ context.Context, code, vendor, itemID string, _ catalog.CodeSource) (*catalog.CodeLink, error) {
	f.linked = append(f.linked, code+"@"+vendor+"->"+itemID)
	return &catalog.CodeLink{Code: code, ItemID: itemID}, nil
}

type fakeLedger struct {
	got []ledger.EventInput
	err error
}

func (f *fakeLedger) RecordEvent(_ context.Context, in ledger.EventInput) (*ledger.Event, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.got = append(f.got, in)
	return &ledger.Event{ItemID: in.ItemID}, nil
}

func match(t catalog.MatchType, confident bool) catalog.LookupResult {
	return catalog.LookupResult{MatchType: t, Confident: confident, Candidates: []catalog.Candidate{{ItemID: "rice"}}}
}

func TestScan(t *testing.T) {
	ctx := context.Background()
	purchase := ledger.EventInput{ItemID: "ignored", Type: ledger.EventPurchase, Quantity: 1}

	t.Run("confident hybrid match records and learns", func(t *testing.T) {
		c, l := &fakeCatalog{result: match(catalog.MatchHybrid, true)}, &fakeLedger{}
		res, err := NewService(c, l).Scan(ctx, Request{Code: "V-1", Vendor: "shop", Learn: true, Event: purchase})
		require.NoError(t, err)
		require.Len(t, l.got, 1)
		assert.Equal(t, "rice", l.got[0].ItemID)
		assert.Equal(t, "V-1", l.got[0].CodeUsed)
		assert.Equal(t, "shop", l.got[0].Vendor)
		assert.NotNil(t, res.Event)
		assert.NotNil(t, res.Link)
		assert.Equal(t, []string{"V-1@shop->rice"}, c.linked)
	})

	t.Run("exact match does not relearn", func(t *testing.T) {
		c := &fakeCatalog{result: match(catalog.MatchExact, true)}
		res, err := NewService(c, &fakeLedger{}).Scan(ctx, Request{Code: "V-1", Learn: true, Event: purchase})
		require.NoError(t, err)
		assert.Nil(t, res.Link)
		assert.Empty(t, c.linked)
	})

	t.Run("event fields win over request code and vendor", func(t *testing.T) {
		l := &fakeLedger{}
		ev := purchase
		ev.CodeUsed, ev.Vendor = "C", "other"
		_, err := NewService(&fakeCatalog{result: match(catalog.MatchExact, true)}, l).Scan(ctx, Request{Code: "V-1", Vendor: "shop", Event: ev})
		require.NoError(t, err)
		assert.Equal(t, "C", l.got[0].CodeUsed)
		assert.Equal(t, "other", l.got[0].Vendor)
	})

	t.Run("no confident match records nothing", func(t *testing.T) {
		l := &fakeLedger{}
		res, err := NewService(&fakeCatalog{result: match(catalog.MatchHybrid, false)}, l).Scan(ctx, Request{Text: "rice", Event: purchase})
		require.ErrorIs(t, err, ErrNoConfidentMatch)
		assert.Len(t, res.Match.Candidates, 1)
		assert.Empty(t, l.got)
	})

	t.Run("errors propagate", func(t *testing.T) {
		boom := errors.New("boom")
		_, err := NewService(&fakeCatalog{err: boom}, &fakeLedger{}).Scan(ctx, Request{Text: "rice", Event: purchase})
		require.ErrorIs(t, err, boom)

		c := &fakeCatalog{result: match(catalog.MatchHybrid, true)}
		_, err = NewService(c, &fakeLedger{err: ledger.ErrInsufficientQuantity}).Scan(ctx, Request{Code: "V-1", Learn: true, Event: purchase})
		require.ErrorIs(t, err, ledger.ErrInsufficientQuantity)
		assert.Empty(t, c.linked, "must not learn when recording failed")
	})
}
