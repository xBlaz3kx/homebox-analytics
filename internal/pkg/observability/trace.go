package observability

import (
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// End records *err on the span (if any) and ends it. Meant to be deferred with a named error result:
//
//	ctx, span := tracer.Start(ctx, "pkg.Op")
//	defer observability.End(span, &err)
func End(span trace.Span, err *error) {
	if err != nil && *err != nil {
		span.RecordError(*err)
		span.SetStatus(codes.Error, (*err).Error())
	}
	span.End()
}
