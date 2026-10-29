package telemetry

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

func SetupTracing(ctx context.Context, service string) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	serviceResource, err := resource.New(ctx, resource.WithAttributes(semconv.ServiceName(service)))
	if err != nil {
		return nil, fmt.Errorf("telemetry resource: %w", err)
	}
	providerOptions := []sdktrace.TracerProviderOption{sdktrace.WithResource(serviceResource)}
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" {
		exporter, err := otlptracegrpc.New(ctx)
		if err != nil {
			return nil, fmt.Errorf("otlp exporter: %w", err)
		}
		providerOptions = append(providerOptions, sdktrace.WithBatcher(exporter))
	}
	tracerProvider := sdktrace.NewTracerProvider(providerOptions...)
	otel.SetTracerProvider(tracerProvider)
	return tracerProvider.Shutdown, nil
}

func Tracer() trace.Tracer { return otel.Tracer("orderflow") }

const traceparentHeader = "traceparent"

var traceContextPropagator = propagation.TraceContext{}

func Traceparent(ctx context.Context) string {
	carrier := propagation.MapCarrier{}
	traceContextPropagator.Inject(ctx, carrier)
	return carrier.Get(traceparentHeader)
}

func ContextWithTraceparent(ctx context.Context, traceparent string) context.Context {
	if traceparent == "" {
		return ctx
	}
	return traceContextPropagator.Extract(ctx, propagation.MapCarrier{traceparentHeader: traceparent})
}
