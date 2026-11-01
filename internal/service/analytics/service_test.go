package analytics

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/homebox"
)

func TestBuckets(t *testing.T) {
	from := time.Date(2026, 2, 11, 15, 0, 0, 0, time.UTC) // Wednesday
	to := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	weeks, end, err := buckets(from, to, IntervalWeek)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 2, 9, 0, 0, 0, 0, time.UTC), weeks[0], "weeks start on Monday")
	assert.True(t, !end.Before(to))

	quarters, _, err := buckets(from, to, IntervalQuarter)
	require.NoError(t, err)
	assert.Equal(t, []time.Time{
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
	}, quarters)

	_, _, err = buckets(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), to, IntervalDay)
	assert.ErrorAs(t, err, new(*ValidationError))
}

func monthStarts(n int) []time.Time {
	out := make([]time.Time, n)
	for i := range out {
		out[i] = time.Date(2026, time.Month(i+1), 1, 0, 0, 0, 0, time.UTC)
	}
	return out
}

func key(t time.Time) string { return t.Format(bucketLayout) }

func TestFoldGrouping(t *testing.T) {
	starts := monthStarts(3)
	items := []scopeItem{
		{id: "a", name: "Coffee", groups: []group{{"kitchen", "Kitchen"}, {"drinks", "Drinks"}}},
		{id: "b", name: "Milk", groups: []group{{"kitchen", "Kitchen"}}},
	}
	values := Values{
		"a": {key(starts[0]): 2, key(starts[2]): 1},
		"b": {key(starts[0]): 3},
	}

	byItem := fold(starts, items, values, nil, false, GroupByItem)
	require.Len(t, byItem, 2)
	assert.Equal(t, "Coffee", byItem[0].Name)
	assert.Equal(t, []float64{2, 0, 1}, pointValues(byItem[0]))

	byCategory := fold(starts, items, values, nil, false, GroupByCategory)
	require.Len(t, byCategory, 2)
	assert.Equal(t, "Drinks", byCategory[0].Name)
	assert.Equal(t, []float64{2, 0, 1}, pointValues(byCategory[0]))
	assert.Equal(t, "Kitchen", byCategory[1].Name, "multi-category item counts in each category")
	assert.Equal(t, []float64{5, 0, 1}, pointValues(byCategory[1]))
}

func TestFoldQuantityCarriesForward(t *testing.T) {
	starts := monthStarts(4)
	items := []scopeItem{
		{id: "a", groups: []group{{"total", "Total"}}},
		{id: "b", groups: []group{{"total", "Total"}}},
	}
	values := Values{
		"a": {key(starts[1]): 4},
		"b": {key(starts[2]): 1},
	}
	initial := map[string]float64{"a": 6}

	total := fold(starts, items, values, initial, true, GroupByTotal)
	require.Len(t, total, 1)
	assert.Equal(t, KindTotal, total[0].Kind)
	assert.Equal(t, []float64{6, 4, 5, 5}, pointValues(total[0]))
}

func pointValues(s Series) []float64 {
	out := make([]float64, len(s.Points))
	for i, p := range s.Points {
		out[i] = p.V
	}
	return out
}

type fakeHomebox struct {
	entities []homebox.EntitySummary
	tags     []homebox.Tag
}

func (f *fakeHomebox) ListAllEntities(context.Context, []string) ([]homebox.EntitySummary, error) {
	return f.entities, nil
}

func (f *fakeHomebox) ListTags(context.Context) ([]homebox.Tag, error) { return f.tags, nil }

type fakeRepo struct {
	gotItems []string
	values   Values
}

func (f *fakeRepo) Aggregate(_ context.Context, ids []string, _ Metric, _ Interval, _ string, _, _ time.Time) (Values, error) {
	f.gotItems = ids
	return f.values, nil
}

func (f *fakeRepo) QuantityBefore(context.Context, []string, time.Time) (map[string]float64, error) {
	return nil, nil
}

func TestSeriesScope(t *testing.T) {
	hb := &fakeHomebox{
		tags: []homebox.Tag{{ID: "t1", Name: "Cleaning"}, {ID: "t2", Name: "Food"}},
		entities: []homebox.EntitySummary{
			{ID: "soap", Name: "Soap", Tags: []homebox.Tag{{ID: "t1"}}},
			{ID: "rice", Name: "Rice", Tags: []homebox.Tag{{ID: "t2"}}},
			{ID: "tape", Name: "Tape"},
		},
	}
	repo := &fakeRepo{values: Values{}}
	svc := NewService(repo, hb)
	svc.now = func() time.Time { return time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC) }

	chart, err := svc.Series(context.Background(), Query{Categories: []string{AllCategories}, Interval: IntervalQuarter})
	require.NoError(t, err)
	assert.Equal(t, GroupByCategory, chart.GroupBy, "categories=all defaults to per-category series")
	assert.Equal(t, []string{"rice", "soap"}, repo.gotItems)
	require.Len(t, chart.Series, 2)
	assert.Equal(t, "Cleaning", chart.Series[0].Name)
	assert.Equal(t, "Food", chart.Series[1].Name)

	chart, err = svc.Series(context.Background(), Query{Items: []string{"tape"}, Categories: []string{"t1"}, GroupBy: GroupByCategory})
	require.NoError(t, err)
	assert.Equal(t, []string{"soap", "tape"}, repo.gotItems)
	require.Len(t, chart.Series, 2)
	assert.Equal(t, "Cleaning", chart.Series[0].Name)
	assert.Equal(t, UncategorizedKey, chart.Series[1].Key)

	_, err = svc.Series(context.Background(), Query{Items: []string{"missing"}})
	assert.ErrorAs(t, err, new(*ValidationError))

	_, err = svc.Series(context.Background(), Query{})
	assert.ErrorAs(t, err, new(*ValidationError))
}
