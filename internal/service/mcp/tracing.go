package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/observability"
)

var tracer = otel.Tracer("github.com/xBlaz3kx/homebox-analytics/internal/service/mcp")

// tracing starts a span per incoming MCP request, named and attributed after the OpenTelemetry MCP
// semantic conventions: "{mcp.method.name} {target}", e.g. "tools/call record_event". Neither the
// MCP Go SDK nor opentelemetry-go-contrib ship MCP instrumentation, and the mcp.* / gen_ai.* keys
// are not in the semconv Go package yet, so those keys are spelled out here.
func tracing() sdk.Middleware {
	return func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, req sdk.Request) (res sdk.Result, err error) {
			name := method
			attrs := []attribute.KeyValue{attribute.String("mcp.method.name", method)}
			if sr, ok := req.(interface{ ProtocolVersion() string }); ok && sr.ProtocolVersion() != "" {
				attrs = append(attrs, attribute.String("mcp.protocol.version", sr.ProtocolVersion()))
			}
			if p, ok := req.GetParams().(*sdk.CallToolParamsRaw); ok && p != nil {
				name += " " + p.Name
				attrs = append(attrs,
					attribute.String("gen_ai.tool.name", p.Name),
					attribute.String("gen_ai.operation.name", "execute_tool"))
			}

			ctx, span := tracer.Start(ctx, name, trace.WithSpanKind(trace.SpanKindServer), trace.WithAttributes(attrs...))
			defer func() {
				// Tool failures are reported in the result, not as a protocol error.
				if r, ok := res.(*sdk.CallToolResult); ok && r != nil && r.IsError {
					span.SetAttributes(semconv.ErrorTypeKey.String("tool_error"))
					span.SetStatus(codes.Error, "tool error")
				}
				observability.End(span, &err)
			}()
			return next(ctx, method, req)
		}
	}
}
