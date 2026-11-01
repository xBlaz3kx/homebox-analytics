// Package observability configures OpenTelemetry traces, metrics and logs exported over OTLP,
// and bridges zap logs into the OTel log pipeline.
package observability

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelzap"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Config struct {
	Enabled     bool
	ServiceName string
	Version     string
	Environment string
	// Endpoint overrides OTEL_EXPORTER_OTLP_ENDPOINT when set (host:port for grpc, URL for http).
	Endpoint    string
	Protocol    string // "grpc" or "http"
	Insecure    bool
	SampleRatio float64
}

// ShutdownFunc flushes and stops all providers.
type ShutdownFunc func(context.Context) error

// Setup installs global tracer, meter and logger providers and propagators, and returns a zap
// logger that also exports to OTel. When disabled, globals stay no-op and the base logger is returned.
func Setup(ctx context.Context, cfg Config, base *zap.Logger) (*zap.Logger, ShutdownFunc, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	if !cfg.Enabled {
		return base, func(context.Context) error { return nil }, nil
	}

	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithHost(),
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.Version),
			attribute.String("deployment.environment.name", cfg.Environment),
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("build resource: %w", err)
	}

	var shutdowns []func(context.Context) error
	shutdown := func(ctx context.Context) error {
		var errs []error
		for i := len(shutdowns) - 1; i >= 0; i-- {
			errs = append(errs, shutdowns[i](ctx))
		}
		return errors.Join(errs...)
	}
	fail := func(err error) (*zap.Logger, ShutdownFunc, error) {
		_ = shutdown(ctx)
		return nil, nil, err
	}

	traceExp, metricExp, logExp, err := exporters(ctx, cfg)
	if err != nil {
		return fail(err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(traceExp),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))),
	)
	shutdowns = append(shutdowns, tp.Shutdown)
	otel.SetTracerProvider(tp)

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp, sdkmetric.WithInterval(15*time.Second))),
	)
	shutdowns = append(shutdowns, mp.Shutdown)
	otel.SetMeterProvider(mp)

	lp := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExp)),
	)
	shutdowns = append(shutdowns, lp.Shutdown)
	otel.SetLoggerProvider(lp)

	if err := runtime.Start(runtime.WithMeterProvider(mp)); err != nil {
		return fail(fmt.Errorf("start runtime metrics: %w", err))
	}

	otelCore := otelzap.NewCore(cfg.ServiceName, otelzap.WithLoggerProvider(lp), otelzap.WithVersion(cfg.Version))
	logger := base.WithOptions(zap.WrapCore(func(c zapcore.Core) zapcore.Core {
		return zapcore.NewTee(c, otelCore)
	}))

	return logger, shutdown, nil
}

func exporters(ctx context.Context, cfg Config) (sdktrace.SpanExporter, sdkmetric.Exporter, sdklog.Exporter, error) {
	switch cfg.Protocol {
	case "", "grpc":
		var (
			to []otlptracegrpc.Option
			mo []otlpmetricgrpc.Option
			lo []otlploggrpc.Option
		)
		if cfg.Endpoint != "" {
			to = append(to, otlptracegrpc.WithEndpoint(cfg.Endpoint))
			mo = append(mo, otlpmetricgrpc.WithEndpoint(cfg.Endpoint))
			lo = append(lo, otlploggrpc.WithEndpoint(cfg.Endpoint))
		}
		if cfg.Insecure {
			to = append(to, otlptracegrpc.WithInsecure())
			mo = append(mo, otlpmetricgrpc.WithInsecure())
			lo = append(lo, otlploggrpc.WithInsecure())
		}
		te, err := otlptracegrpc.New(ctx, to...)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("trace exporter: %w", err)
		}
		me, err := otlpmetricgrpc.New(ctx, mo...)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("metric exporter: %w", err)
		}
		le, err := otlploggrpc.New(ctx, lo...)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("log exporter: %w", err)
		}
		return te, me, le, nil

	case "http", "http/protobuf":
		var (
			to []otlptracehttp.Option
			mo []otlpmetrichttp.Option
			lo []otlploghttp.Option
		)
		if cfg.Endpoint != "" {
			to = append(to, otlptracehttp.WithEndpointURL(cfg.Endpoint+"/v1/traces"))
			mo = append(mo, otlpmetrichttp.WithEndpointURL(cfg.Endpoint+"/v1/metrics"))
			lo = append(lo, otlploghttp.WithEndpointURL(cfg.Endpoint+"/v1/logs"))
		}
		if cfg.Insecure {
			to = append(to, otlptracehttp.WithInsecure())
			mo = append(mo, otlpmetrichttp.WithInsecure())
			lo = append(lo, otlploghttp.WithInsecure())
		}
		te, err := otlptracehttp.New(ctx, to...)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("trace exporter: %w", err)
		}
		me, err := otlpmetrichttp.New(ctx, mo...)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("metric exporter: %w", err)
		}
		le, err := otlploghttp.New(ctx, lo...)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("log exporter: %w", err)
		}
		return te, me, le, nil

	default:
		return nil, nil, nil, fmt.Errorf("unsupported otlp protocol %q", cfg.Protocol)
	}
}
