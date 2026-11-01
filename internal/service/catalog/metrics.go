package catalog

import (
	"context"
	"sync/atomic"

	"go.opentelemetry.io/otel/metric"
)

type metrics struct {
	lookups        metric.Int64Counter
	searchDuration metric.Float64Histogram
	syncDuration   metric.Float64Histogram
	syncItems      metric.Int64Counter
	lastSync       atomic.Int64
}

func newMetrics(meter metric.Meter) (*metrics, error) {
	m := &metrics{}
	var err error

	if m.lookups, err = meter.Int64Counter("hba.lookup.requests",
		metric.WithDescription("Item lookups by match type")); err != nil {
		return nil, err
	}
	if m.searchDuration, err = meter.Float64Histogram("hba.search.duration",
		metric.WithDescription("Hybrid search latency (embedding + query)"), metric.WithUnit("s")); err != nil {
		return nil, err
	}
	if m.syncDuration, err = meter.Float64Histogram("hba.sync.duration",
		metric.WithDescription("Homebox to index sync duration"), metric.WithUnit("s")); err != nil {
		return nil, err
	}
	if m.syncItems, err = meter.Int64Counter("hba.sync.items",
		metric.WithDescription("Items processed by sync, by result")); err != nil {
		return nil, err
	}

	_, err = meter.Int64ObservableGauge("hba.sync.last_success",
		metric.WithDescription("Unix time of the last successful sync"), metric.WithUnit("s"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			if ts := m.lastSync.Load(); ts > 0 {
				o.Observe(ts)
			}
			return nil
		}))
	if err != nil {
		return nil, err
	}

	return m, nil
}
