package otel

import (
	"context"
	"log"
	"os"
	"strings"

	"github.com/docker/model-cli/desktop"
	"github.com/spf13/cobra"
	otel2 "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	trace2 "go.opentelemetry.io/otel/trace"
)

const (
	dockerModelCLI = "docker-model-cli"
	cleanupKey     = "cleanupKey"
)

func initOTel(ctx context.Context) (func(), error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(
			attribute.String("service.name", dockerModelCLI),
			attribute.String("service.version", desktop.Version),
		),
	)
	if err != nil {
		return nil, err
	}

	traceExporter, err := otlptracegrpc.New(ctx)
	if err != nil {
		return nil, err
	}

	tracerProvider := trace.NewTracerProvider(
		trace.WithBatcher(traceExporter),
		trace.WithResource(res),
	)
	otel2.SetTracerProvider(tracerProvider)

	otel2.SetTextMapPropagator(propagation.TraceContext{})

	return func() {
		tracerProvider.Shutdown(ctx)
	}, nil
}

func extractTraceContext(ctx context.Context, traceparent string) context.Context {
	carrier := map[string]string{
		"traceparent": traceparent,
	}

	propagator := otel2.GetTextMapPropagator()
	return propagator.Extract(ctx, propagation.MapCarrier(carrier))
}

func SetupTracing(ctx context.Context) context.Context {
	traceparent := os.Getenv("traceparent")
	if traceparent == "" {
		return ctx
	}

	cleanup, err := initOTel(ctx)
	if err != nil {
		log.Printf("Failed to initialize OpenTelemetry: %v", err)
		return ctx
	}

	parentCtx := extractTraceContext(ctx, traceparent)

	ctx = context.WithValue(parentCtx, cleanupKey, cleanup)

	return ctx
}

func startSpanFromContext(ctx context.Context, operationName string, opts ...trace2.SpanStartOption) (context.Context, trace2.Span) {
	tracer := otel2.Tracer(dockerModelCLI)
	return tracer.Start(ctx, operationName, opts...)
}

func CleanupTracing(ctx context.Context) {
	if cleanup, ok := ctx.Value(cleanupKey).(func()); ok {
		defer cleanup()
	}
}

// Helper function to wrap command execution with a span.
func withSpan(operationName string, fn func(cmd *cobra.Command, args []string) error) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		// Check if tracing is enabled.
		if ctx.Value(cleanupKey) == nil {
			// No tracing, just execute the function.
			return fn(cmd, args)
		}

		// Start a span for this command.
		ctx, span := startSpanFromContext(ctx, operationName,
			trace2.WithAttributes(
				attribute.String("command.name", cmd.Name()),
				attribute.String("command.path", cmd.CommandPath()),
				attribute.String("command.full", strings.Join(os.Args, " ")),
			),
		)
		defer span.End()

		// Update the command context with the new span context.
		cmd.SetContext(ctx)

		// Execute the actual command.
		err := fn(cmd, args)

		// Set span status based on result.
		if err != nil {
			span.SetAttributes(attribute.String("error", err.Error()))
			span.SetStatus(codes.Error, err.Error())
		} else {
			span.SetStatus(codes.Ok, "Command completed successfully")
		}

		return err
	}
}

// Helper function for commands that only have Run (not RunE).
func withSpanRun(operationName string, fn func(cmd *cobra.Command, args []string)) func(cmd *cobra.Command, args []string) {
	return func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()

		// Check if tracing is enabled.
		if ctx.Value(cleanupKey) == nil {
			// No tracing, just execute the function.
			fn(cmd, args)
			return
		}

		// Start a span for this command.
		ctx, span := startSpanFromContext(ctx, operationName,
			trace2.WithAttributes(
				attribute.String("command.name", cmd.Name()),
				attribute.String("command.path", cmd.CommandPath()),
				attribute.String("command.full", strings.Join(os.Args, " ")),
			),
		)
		defer span.End()

		// Update the command context with the new span context.
		cmd.SetContext(ctx)

		// Execute the actual command.
		fn(cmd, args)

		// Mark as successful (since Run doesn't return an error).
		span.SetStatus(codes.Ok, "Command completed successfully")
	}
}

// WrapCommandWithSpan wraps a cobra command with OTEL spans.
func WrapCommandWithSpan(cmd *cobra.Command) *cobra.Command {
	operationName := cmd.Name()

	// Wrap RunE if it exists.
	if cmd.RunE != nil {
		originalRunE := cmd.RunE
		cmd.RunE = withSpan(operationName, originalRunE)
	}

	// Wrap Run if it exists (and RunE doesn't).
	if cmd.Run != nil && cmd.RunE == nil {
		originalRun := cmd.Run
		cmd.Run = withSpanRun(operationName, originalRun)
	}

	// Recursively wrap subcommands.
	for _, subCmd := range cmd.Commands() {
		WrapCommandWithSpan(subCmd)
	}

	return cmd
}
