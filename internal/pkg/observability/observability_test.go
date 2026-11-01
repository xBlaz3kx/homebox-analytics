package observability

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.uber.org/zap"
)

func TestSetupDisabledIsNoop(t *testing.T) {
	base := zap.NewNop()
	before := otel.GetTracerProvider()

	logger, shutdown, err := Setup(context.Background(), Config{Enabled: false}, base)
	require.NoError(t, err)

	assert.Same(t, base, logger)
	assert.Equal(t, before, otel.GetTracerProvider())
	assert.NoError(t, shutdown(context.Background()))
}

func TestSetupRejectsUnknownProtocol(t *testing.T) {
	_, _, err := Setup(context.Background(), Config{Enabled: true, ServiceName: "t", Protocol: "udp", SampleRatio: 1}, zap.NewNop())
	assert.ErrorContains(t, err, "unsupported otlp protocol")
}
