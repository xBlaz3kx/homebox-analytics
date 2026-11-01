package analytics

import (
	"slices"
	"time"
)

type Interval string

const (
	IntervalDay     Interval = "day"
	IntervalWeek    Interval = "week"
	IntervalMonth   Interval = "month"
	IntervalQuarter Interval = "quarter"
	IntervalYear    Interval = "year"
)

type Metric string

const (
	MetricPurchased Metric = "purchased"
	MetricConsumed  Metric = "consumed"
	MetricSpend     Metric = "spend"
	MetricQuantity  Metric = "quantity"
	MetricEvents    Metric = "events"
)

type GroupBy string

const (
	GroupByItem     GroupBy = "item"
	GroupByCategory GroupBy = "category"
	GroupByTotal    GroupBy = "total"
)

// AllCategories selects every Homebox tag when passed as the only category.
const AllCategories = "all"

// UncategorizedKey groups items without a matching category.
const UncategorizedKey = "uncategorized"

const maxBuckets = 2000

// ValidationError marks invalid query input.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

// Query selects items (directly and/or via categories) and how to aggregate their events.
type Query struct {
	Items      []string  `json:"items"`
	Categories []string  `json:"categories"`
	GroupBy    GroupBy   `json:"group_by"`
	Metric     Metric    `json:"metric"`
	Interval   Interval  `json:"interval"`
	From       time.Time `json:"from"`
	To         time.Time `json:"to"`
	// Timezone used for bucket boundaries (IANA name), default UTC.
	Timezone string `json:"timezone"`
}

func (q Query) allCategories() bool {
	return slices.Contains(q.Categories, AllCategories)
}

type Point struct {
	T time.Time `json:"t"`
	V float64   `json:"v"`
}

type SeriesKind string

const (
	KindItem     SeriesKind = "item"
	KindCategory SeriesKind = "category"
	KindTotal    SeriesKind = "total"
)

type Series struct {
	Key    string     `json:"key"`
	Kind   SeriesKind `json:"kind"`
	Name   string     `json:"name"`
	Points []Point    `json:"points"`
}

type Chart struct {
	Interval Interval  `json:"interval"`
	Metric   Metric    `json:"metric"`
	GroupBy  GroupBy   `json:"group_by"`
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Timezone string    `json:"timezone"`
	Series   []Series  `json:"series"`
}
