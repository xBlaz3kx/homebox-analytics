package observability

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestEnd(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tracer := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)).Tracer("test")

	op := func(fail bool) (err error) {
		_, span := tracer.Start(context.Background(), "op")
		defer End(span, &err)
		if fail {
			return errors.New("boom")
		}
		return nil
	}

	require.NoError(t, op(false))
	require.Error(t, op(true))

	spans := rec.Ended()
	require.Len(t, spans, 2)
	assert.Equal(t, codes.Unset, spans[0].Status().Code)
	assert.Empty(t, spans[0].Events())
	assert.Equal(t, codes.Error, spans[1].Status().Code)
	assert.Equal(t, "boom", spans[1].Status().Description)
	require.Len(t, spans[1].Events(), 1)
	assert.Equal(t, "exception", spans[1].Events()[0].Name)
}
