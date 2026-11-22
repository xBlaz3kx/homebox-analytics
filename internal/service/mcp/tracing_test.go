package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestTracingSpans(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	f := newFixture(t)
	f.call(t, "list_tags", nil, nil)
	f.call(t, "record_event", map[string]any{"item_id": "rice", "type": "consume", "quantity": -1}, nil)

	spans := map[string]sdktrace.ReadOnlySpan{}
	for _, s := range rec.Ended() {
		spans[s.Name()] = s
	}
	require.Contains(t, spans, "tools/call list_tags")
	require.Contains(t, spans, "tools/call record_event")

	ok := spans["tools/call list_tags"]
	assert.Equal(t, trace.SpanKindServer, ok.SpanKind())
	assert.Equal(t, codes.Unset, ok.Status().Code)
	attrs := attribute.NewSet(ok.Attributes()...)
	v, _ := attrs.Value("gen_ai.tool.name")
	assert.Equal(t, "list_tags", v.AsString())
	v, _ = attrs.Value("mcp.method.name")
	assert.Equal(t, "tools/call", v.AsString())

	failed := spans["tools/call record_event"]
	assert.Equal(t, codes.Error, failed.Status().Code)
	failedAttrs := attribute.NewSet(failed.Attributes()...)
	v, _ = failedAttrs.Value("error.type")
	assert.Equal(t, "tool_error", v.AsString())
}
