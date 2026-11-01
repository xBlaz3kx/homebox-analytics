package ledger_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/homebox"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/ledger"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/ledger/mocks"
)

type deps struct {
	repo *mocks.MockRepository
	tx   *mocks.MockTx
	inv  *mocks.MockInventory
	svc  *ledger.Service
}

func newDeps(t *testing.T, homeboxQty float64) deps {
	d := deps{repo: mocks.NewMockRepository(t), tx: mocks.NewMockTx(t), inv: mocks.NewMockInventory(t)}
	svc, err := ledger.NewService(d.repo, d.inv, zap.NewNop())
	require.NoError(t, err)
	d.svc = svc

	d.repo.EXPECT().BeginItem(mock.Anything, "item").Return(d.tx, nil)
	d.inv.EXPECT().GetEntity(mock.Anything, "item").
		Return(&homebox.Entity{EntitySummary: homebox.EntitySummary{ID: "item", Quantity: homeboxQty}}, nil)
	return d
}

func echoEvent(_ context.Context, e ledger.Event) (*ledger.Event, error) {
	e.ID = "ev"
	return &e, nil
}

func TestRecordEventConsume(t *testing.T) {
	d := newDeps(t, 5)

	d.tx.EXPECT().InsertEvent(mock.Anything, mock.MatchedBy(func(e ledger.Event) bool {
		return e.Type == ledger.EventConsume && e.Delta == -2 && e.QuantityAfter == 3
	})).RunAndReturn(echoEvent)
	d.inv.EXPECT().SetQuantity(mock.Anything, "item", 3.0).Return(&homebox.Entity{}, nil)
	d.tx.EXPECT().Commit(mock.Anything).Return(nil)

	ev, err := d.svc.RecordEvent(context.Background(), ledger.EventInput{ItemID: "item", Type: ledger.EventConsume, Quantity: 2})
	require.NoError(t, err)
	assert.Equal(t, 3.0, ev.QuantityAfter)
	assert.False(t, ev.OccurredAt.IsZero())
}

func TestRecordEventRejectsOverConsumption(t *testing.T) {
	d := newDeps(t, 1)
	d.tx.EXPECT().Rollback(mock.Anything).Return(nil)

	_, err := d.svc.RecordEvent(context.Background(), ledger.EventInput{ItemID: "item", Type: ledger.EventConsume, Quantity: 2})
	assert.ErrorIs(t, err, ledger.ErrInsufficientQuantity)
}

func TestRecordEventHomeboxFailureRollsBack(t *testing.T) {
	d := newDeps(t, 1)
	d.tx.EXPECT().InsertEvent(mock.Anything, mock.Anything).RunAndReturn(echoEvent)
	d.inv.EXPECT().SetQuantity(mock.Anything, "item", 4.0).Return(nil, errors.New("boom"))
	d.tx.EXPECT().Rollback(mock.Anything).Return(nil)

	_, err := d.svc.RecordEvent(context.Background(), ledger.EventInput{ItemID: "item", Type: ledger.EventPurchase, Quantity: 3})
	assert.ErrorContains(t, err, "update homebox quantity")
}

func TestRecordEventCommitFailureRestoresHomebox(t *testing.T) {
	d := newDeps(t, 1)
	d.tx.EXPECT().InsertEvent(mock.Anything, mock.Anything).RunAndReturn(echoEvent)
	d.inv.EXPECT().SetQuantity(mock.Anything, "item", 4.0).Return(&homebox.Entity{}, nil).Once()
	d.tx.EXPECT().Commit(mock.Anything).Return(errors.New("conn lost"))
	d.inv.EXPECT().SetQuantity(mock.Anything, "item", 1.0).Return(&homebox.Entity{}, nil).Once()
	d.tx.EXPECT().Rollback(mock.Anything).Return(nil)

	_, err := d.svc.RecordEvent(context.Background(), ledger.EventInput{ItemID: "item", Type: ledger.EventPurchase, Quantity: 3, OccurredAt: time.Now()})
	assert.ErrorContains(t, err, "commit")
}

func TestReconcileApply(t *testing.T) {
	d := newDeps(t, 10)

	d.tx.EXPECT().InsertReconciliation(mock.Anything, mock.MatchedBy(func(r ledger.Reconciliation) bool {
		return r.HomeboxQty == 10 && r.CountedQty == 7 && r.Delta == -3 && r.Applied
	})).RunAndReturn(func(_ context.Context, r ledger.Reconciliation) (*ledger.Reconciliation, error) { return &r, nil })
	d.tx.EXPECT().InsertEvent(mock.Anything, mock.MatchedBy(func(e ledger.Event) bool {
		return e.Type == ledger.EventReconcile && e.Delta == -3 && e.QuantityAfter == 7
	})).RunAndReturn(echoEvent)
	d.inv.EXPECT().SetQuantity(mock.Anything, "item", 7.0).Return(&homebox.Entity{}, nil)
	d.tx.EXPECT().Commit(mock.Anything).Return(nil)

	rec, err := d.svc.Reconcile(context.Background(), ledger.ReconcileInput{ItemID: "item", Counted: 7, Apply: true})
	require.NoError(t, err)
	assert.True(t, rec.Applied)
	require.NotNil(t, rec.Event)
}

func TestReconcileDryRunDoesNotTouchHomebox(t *testing.T) {
	d := newDeps(t, 10)

	d.tx.EXPECT().InsertReconciliation(mock.Anything, mock.MatchedBy(func(r ledger.Reconciliation) bool {
		return r.Delta == 2 && !r.Applied
	})).RunAndReturn(func(_ context.Context, r ledger.Reconciliation) (*ledger.Reconciliation, error) { return &r, nil })
	d.tx.EXPECT().Commit(mock.Anything).Return(nil)

	rec, err := d.svc.Reconcile(context.Background(), ledger.ReconcileInput{ItemID: "item", Counted: 12})
	require.NoError(t, err)
	assert.False(t, rec.Applied)
	assert.Nil(t, rec.Event)
}

func TestEventInputDelta(t *testing.T) {
	_, err := ledger.EventInput{Type: ledger.EventPurchase, Quantity: -1}.Delta()
	assert.ErrorAs(t, err, new(*ledger.ValidationError))

	d, err := ledger.EventInput{Type: ledger.EventAdjust, Quantity: -2}.Delta()
	require.NoError(t, err)
	assert.Equal(t, -2.0, d)

	_, err = ledger.EventInput{Type: "reconcile", Quantity: 1}.Delta()
	assert.ErrorAs(t, err, new(*ledger.ValidationError), "reconcile events are only created through Reconcile")
}
