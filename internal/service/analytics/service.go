package analytics

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/homebox"
)

var tracer = otel.Tracer("github.com/xBlaz3kx/homebox-analytics/internal/service/analytics")

// Homebox is the subset of the Homebox client used to resolve chart scope.
type Homebox interface {
	ListAllEntities(ctx context.Context, tagIDs []string) ([]homebox.EntitySummary, error)
	ListTags(ctx context.Context) ([]homebox.Tag, error)
}

type Service struct {
	repo    Repository
	homebox Homebox
	now     func() time.Time
}

func NewService(repo Repository, hb Homebox) *Service {
	return &Service{repo: repo, homebox: hb, now: time.Now}
}

// group is a category (or the total) an item contributes to.
type group struct {
	key  string
	name string
}

type scopeItem struct {
	id     string
	name   string
	groups []group
}

// Series builds a chart for the items selected directly and via categories.
func (s *Service) Series(ctx context.Context, q Query) (_ *Chart, err error) {
	ctx, span := tracer.Start(ctx, "analytics.Series")
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	loc, err := s.normalize(&q)
	if err != nil {
		return nil, err
	}
	span.SetAttributes(
		attribute.String("metric", string(q.Metric)),
		attribute.String("interval", string(q.Interval)),
		attribute.String("group_by", string(q.GroupBy)),
		attribute.Int("items.requested", len(q.Items)),
		attribute.Int("categories.requested", len(q.Categories)),
	)

	starts, end, err := buckets(q.From.In(loc), q.To.In(loc), q.Interval)
	if err != nil {
		return nil, err
	}

	items, err := s.scope(ctx, q)
	if err != nil {
		return nil, err
	}
	span.SetAttributes(attribute.Int("items.resolved", len(items)))

	chart := &Chart{
		Interval: q.Interval, Metric: q.Metric, GroupBy: q.GroupBy,
		From: starts[0], To: end, Timezone: loc.String(), Series: []Series{},
	}
	if len(items) == 0 {
		return chart, nil
	}

	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.id
	}

	values, err := s.repo.Aggregate(ctx, ids, q.Metric, q.Interval, loc.String(), starts[0], end)
	if err != nil {
		return nil, fmt.Errorf("aggregate events: %w", err)
	}

	var initial map[string]float64
	if q.Metric == MetricQuantity {
		if initial, err = s.repo.QuantityBefore(ctx, ids, starts[0]); err != nil {
			return nil, fmt.Errorf("load starting quantities: %w", err)
		}
	}

	chart.Series = fold(starts, items, values, initial, q.Metric == MetricQuantity, q.GroupBy)
	return chart, nil
}

// normalize validates the query, applies defaults and returns the bucket timezone.
func (s *Service) normalize(q *Query) (*time.Location, error) {
	if len(q.Items) == 0 && len(q.Categories) == 0 {
		return nil, &ValidationError{"at least one of items or categories is required"}
	}

	if q.Interval == "" {
		q.Interval = IntervalWeek
	}
	if !slices.Contains([]Interval{IntervalDay, IntervalWeek, IntervalMonth, IntervalQuarter, IntervalYear}, q.Interval) {
		return nil, &ValidationError{"interval must be one of day, week, month, quarter, year"}
	}

	if q.Metric == "" {
		q.Metric = MetricConsumed
	}
	if _, ok := metricExpr[q.Metric]; !ok {
		return nil, &ValidationError{"metric must be one of purchased, consumed, spend, quantity, events"}
	}

	if q.GroupBy == "" {
		q.GroupBy = GroupByItem
		if q.allCategories() {
			q.GroupBy = GroupByCategory
		}
	}
	if !slices.Contains([]GroupBy{GroupByItem, GroupByCategory, GroupByTotal}, q.GroupBy) {
		return nil, &ValidationError{"group_by must be one of item, category, total"}
	}

	if q.Timezone == "" {
		q.Timezone = "UTC"
	}
	loc, err := time.LoadLocation(q.Timezone)
	if err != nil {
		return nil, &ValidationError{fmt.Sprintf("unknown timezone %q", q.Timezone)}
	}

	if q.To.IsZero() {
		q.To = s.now()
	}
	if q.From.IsZero() {
		q.From = q.To.AddDate(-1, 0, 0)
	}
	if !q.From.Before(q.To) {
		return nil, &ValidationError{"from must be before to"}
	}
	return loc, nil
}

// scope resolves the requested items and categories to items with their chart groups.
// Category membership is read live from Homebox, so relabeling is reflected immediately.
func (s *Service) scope(ctx context.Context, q Query) ([]scopeItem, error) {
	entities, err := s.homebox.ListAllEntities(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("list homebox items: %w", err)
	}
	byID := make(map[string]homebox.EntitySummary, len(entities))
	for _, e := range entities {
		byID[e.ID] = e
	}

	tagNames := map[string]string{}
	var categories []string
	if len(q.Categories) > 0 || q.GroupBy == GroupByCategory {
		tags, err := s.homebox.ListTags(ctx)
		if err != nil {
			return nil, fmt.Errorf("list homebox tags: %w", err)
		}
		for _, t := range tags {
			tagNames[t.ID] = t.Name
		}

		if q.allCategories() {
			for _, t := range tags {
				categories = append(categories, t.ID)
			}
		} else {
			for _, c := range q.Categories {
				if _, ok := tagNames[c]; !ok {
					return nil, &ValidationError{fmt.Sprintf("unknown category %q", c)}
				}
				categories = append(categories, c)
			}
		}
	}

	selected := map[string]homebox.EntitySummary{}
	for _, id := range q.Items {
		e, ok := byID[id]
		if !ok {
			return nil, &ValidationError{fmt.Sprintf("unknown item %q", id)}
		}
		selected[id] = e
	}
	for _, e := range entities {
		if slices.ContainsFunc(e.TagIDs(), func(t string) bool { return slices.Contains(categories, t) }) {
			selected[e.ID] = e
		}
	}

	items := make([]scopeItem, 0, len(selected))
	for _, e := range selected {
		it := scopeItem{id: e.ID, name: e.Name}

		switch q.GroupBy {
		case GroupByTotal:
			it.groups = []group{{key: "total", name: "Total"}}
		case GroupByCategory:
			for _, t := range e.TagIDs() {
				if len(categories) == 0 || slices.Contains(categories, t) {
					it.groups = append(it.groups, group{key: t, name: tagNames[t]})
				}
			}
			if len(it.groups) == 0 {
				it.groups = []group{{key: UncategorizedKey, name: "Uncategorized"}}
			}
		}
		items = append(items, it)
	}

	slices.SortFunc(items, func(a, b scopeItem) int { return strings.Compare(a.id, b.id) })
	return items, nil
}

// fold turns per-item bucket values into series grouped by item, category or total. For
// quantity, each item's value is carried forward across empty buckets before summing.
func fold(starts []time.Time, items []scopeItem, values Values, initial map[string]float64, carry bool, groupBy GroupBy) []Series {
	perItem := make(map[string][]float64, len(items))
	for _, it := range items {
		row := make([]float64, len(starts))
		last := initial[it.id]
		for i, b := range starts {
			v, ok := values[it.id][b.Format(bucketLayout)]
			switch {
			case ok:
				last = v
				row[i] = v
			case carry:
				row[i] = last
			}
		}
		perItem[it.id] = row
	}

	toPoints := func(row []float64) []Point {
		pts := make([]Point, len(starts))
		for i, b := range starts {
			pts[i] = Point{T: b, V: row[i]}
		}
		return pts
	}

	var series []Series
	if groupBy == GroupByItem {
		for _, it := range items {
			series = append(series, Series{Key: it.id, Kind: KindItem, Name: it.name, Points: toPoints(perItem[it.id])})
		}
	} else {
		kind := KindCategory
		if groupBy == GroupByTotal {
			kind = KindTotal
		}

		sums := map[string][]float64{}
		names := map[string]string{}
		for _, it := range items {
			for _, g := range it.groups {
				if sums[g.key] == nil {
					sums[g.key] = make([]float64, len(starts))
					names[g.key] = g.name
				}
				for i, v := range perItem[it.id] {
					sums[g.key][i] += v
				}
			}
		}
		for key, row := range sums {
			series = append(series, Series{Key: key, Kind: kind, Name: names[key], Points: toPoints(row)})
		}
	}

	slices.SortFunc(series, func(a, b Series) int {
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.Key, b.Key)
	})
	return series
}
