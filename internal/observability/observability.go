package observability

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"example.com/llm-chat-web/internal/buildinfo"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

const (
	DefaultServiceName = "pyttechat"

	ComponentCLI          = "cli"
	ComponentWeb          = "web"
	ComponentLLMProxy     = "llm_proxy"
	ComponentFakeProvider = "fake_provider"

	ChatTurnStatusCompleted = "completed"
	ChatTurnStatusCancelled = "cancelled"
	ChatTurnStatusFailed    = "failed"

	instrumentationName = "example.com/llm-chat-web/internal/observability"
)

type Config struct {
	ServiceName     string
	ServiceVersion  string
	Environment     string
	OTLPEndpoint    string
	TracesEndpoint  string
	MetricsEndpoint string
	Enabled         bool
	TracesEnabled   bool
	MetricsEnabled  bool
}

type ShutdownFunc func(context.Context) error

var (
	runtimeMu      sync.RWMutex
	runtimeEnabled bool

	instrumentMu sync.Mutex
	instruments  metricInstruments
)

type metricInstruments struct {
	initialized    bool
	turnsStarted   metric.Int64Counter
	turnsCompleted metric.Int64Counter
	turnsCancelled metric.Int64Counter
	turnsFailed    metric.Int64Counter
	streamEvents   metric.Int64Counter
	llmDurationMS  metric.Float64Histogram
	turnDurationMS metric.Float64Histogram
}

type contextKey string

const componentContextKey contextKey = "component"

func FromEnv(info buildinfo.Info) Config {
	cfg := Config{
		ServiceName:     envString("OTEL_SERVICE_NAME", DefaultServiceName),
		ServiceVersion:  strings.TrimSpace(info.Version),
		OTLPEndpoint:    strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")),
		TracesEndpoint:  strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")),
		MetricsEndpoint: strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT")),
	}
	if sdkDisabled() {
		return cfg
	}

	cfg.TracesEnabled = cfg.OTLPEndpoint != "" || cfg.TracesEndpoint != ""
	cfg.MetricsEnabled = cfg.OTLPEndpoint != "" || cfg.MetricsEndpoint != ""
	cfg.Enabled = cfg.TracesEnabled || cfg.MetricsEnabled
	return cfg
}

func Init(ctx context.Context, cfg Config) (ShutdownFunc, error) {
	cfg = normalizeConfig(cfg)
	previous := captureGlobals()

	if !cfg.Enabled || sdkDisabled() {
		setEnabled(false)
		return onceShutdown(func(context.Context) error { return nil }), nil
	}
	if err := validateConfig(cfg); err != nil {
		setEnabled(false)
		return nil, err
	}

	res, err := resourceForConfig(ctx, cfg)
	if err != nil {
		setEnabled(false)
		return nil, err
	}

	var tracerProvider *sdktrace.TracerProvider
	var meterProvider *sdkmetric.MeterProvider
	if cfg.TracesEnabled {
		exporter, err := otlptracegrpc.New(ctx, traceExporterOptions(cfg)...)
		if err != nil {
			setEnabled(false)
			return nil, fmt.Errorf("initialize OTLP trace exporter: %w", err)
		}
		tracerProvider = sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithBatcher(exporter),
		)
		otel.SetTracerProvider(tracerProvider)
	} else {
		otel.SetTracerProvider(tracenoop.NewTracerProvider())
	}

	if cfg.MetricsEnabled {
		exporter, err := otlpmetricgrpc.New(ctx, metricExporterOptions(cfg)...)
		if err != nil {
			if tracerProvider != nil {
				_ = tracerProvider.Shutdown(ctx)
			}
			previous.restore()
			setEnabled(false)
			return nil, fmt.Errorf("initialize OTLP metric exporter: %w", err)
		}
		meterProvider = sdkmetric.NewMeterProvider(
			sdkmetric.WithResource(res),
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)),
		)
		otel.SetMeterProvider(meterProvider)
	} else {
		otel.SetMeterProvider(metricnoop.NewMeterProvider())
	}

	otel.SetTextMapPropagator(propagation.TraceContext{})
	setEnabled(true)

	return onceShutdown(func(ctx context.Context) error {
		var err error
		if tracerProvider != nil {
			err = errors.Join(err, tracerProvider.Shutdown(ctx))
		}
		if meterProvider != nil {
			err = errors.Join(err, meterProvider.Shutdown(ctx))
		}
		previous.restore()
		setEnabled(false)
		return err
	}), nil
}

func HTTPMiddleware(next http.Handler) http.Handler {
	if !isEnabled() {
		return next
	}
	return otelhttp.NewHandler(next, "http.request",
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return RouteName(r.Method, r.URL.Path)
		}),
	)
}

func HTTPClientTransport(base http.RoundTripper) http.RoundTripper {
	if !isEnabled() {
		return base
	}
	return otelhttp.NewTransport(base)
}

func RouteName(method, path string) string {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		method = "UNKNOWN"
	}
	path = strings.TrimSpace(path)
	if path == "" {
		path = "/"
	}

	switch {
	case strings.HasPrefix(path, "/assets/"):
		return method + " /assets/*"
	case path == "/favicon.ico":
		return method + " /favicon.ico"
	case path == "/":
		return method + " /"
	case path == "/login":
		return method + " /login"
	case path == "/register":
		return method + " /register"
	case path == "/logout":
		return method + " /logout"
	case path == "/chat/turns":
		return method + " /chat/turns"
	case strings.HasPrefix(path, "/chat/turns/"):
		parts := strings.Split(strings.TrimPrefix(path, "/chat/turns/"), "/")
		if len(parts) == 2 && parts[0] != "" {
			switch parts[1] {
			case "events":
				return method + " /chat/turns/{turn_id}/events"
			case "abort":
				return method + " /chat/turns/{turn_id}/abort"
			}
		}
	}
	return method + " unmatched"
}

func ContextWithComponent(ctx context.Context, component string) context.Context {
	return context.WithValue(ctx, componentContextKey, sanitizeComponent(component))
}

func StartSpan(ctx context.Context, name, component string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	if !isEnabled() {
		return tracenoop.NewTracerProvider().Tracer(instrumentationName).Start(ctx, name)
	}
	component = componentOrContext(ctx, component)
	spanAttrs := make([]attribute.KeyValue, 0, len(attrs)+1)
	spanAttrs = append(spanAttrs, attribute.String("component", component))
	spanAttrs = append(spanAttrs, attrs...)
	return otel.Tracer(instrumentationName).Start(ctx, name, trace.WithAttributes(spanAttrs...))
}

func RecordSpanError(span trace.Span, err error) {
	if span == nil || err == nil {
		return
	}
	span.RecordError(err, trace.WithAttributes(attribute.String("error.type", errorType(err))))
	span.SetStatus(codes.Error, "error")
}

func SetSpanStatus(span trace.Span, status string) {
	if span == nil || status == "" {
		return
	}
	span.SetAttributes(attribute.String("chat.turn.status", sanitizeStatus(status)))
}

func ChatTurnStarted(ctx context.Context) {
	if !isEnabled() {
		return
	}
	metrics := ensureInstruments()
	if metrics.turnsStarted != nil {
		metrics.turnsStarted.Add(ctx, 1, metric.WithAttributes(componentAttribute(ctx)))
	}
}

func ChatTurnCompleted(ctx context.Context, startedAt time.Time) {
	if !isEnabled() {
		return
	}
	metrics := ensureInstruments()
	attrs := metric.WithAttributes(componentAttribute(ctx), attribute.String("status", ChatTurnStatusCompleted))
	if metrics.turnsCompleted != nil {
		metrics.turnsCompleted.Add(ctx, 1, attrs)
	}
	recordTurnDuration(ctx, metrics, startedAt, ChatTurnStatusCompleted)
}

func ChatTurnCancelled(ctx context.Context, startedAt time.Time) {
	if !isEnabled() {
		return
	}
	metrics := ensureInstruments()
	attrs := metric.WithAttributes(componentAttribute(ctx), attribute.String("status", ChatTurnStatusCancelled))
	if metrics.turnsCancelled != nil {
		metrics.turnsCancelled.Add(ctx, 1, attrs)
	}
	recordTurnDuration(ctx, metrics, startedAt, ChatTurnStatusCancelled)
}

func ChatTurnFailed(ctx context.Context, startedAt time.Time, err error) {
	if !isEnabled() {
		return
	}
	metrics := ensureInstruments()
	attrs := []attribute.KeyValue{
		componentAttribute(ctx),
		attribute.String("status", ChatTurnStatusFailed),
	}
	if err != nil {
		attrs = append(attrs, attribute.String("error.type", errorType(err)))
	}
	if metrics.turnsFailed != nil {
		metrics.turnsFailed.Add(ctx, 1, metric.WithAttributes(attrs...))
	}
	recordTurnDuration(ctx, metrics, startedAt, ChatTurnStatusFailed)
}

func LLMStreamEvent(ctx context.Context, component, eventType string) {
	if !isEnabled() {
		return
	}
	metrics := ensureInstruments()
	if metrics.streamEvents == nil {
		return
	}
	metrics.streamEvents.Add(ctx, 1, metric.WithAttributes(
		attribute.String("component", sanitizeComponent(componentOrContext(ctx, component))),
		attribute.String("event_type", sanitizeEventType(eventType)),
	))
}

func RecordLLMRequestDuration(ctx context.Context, component string, startedAt time.Time, status string) {
	if !isEnabled() {
		return
	}
	metrics := ensureInstruments()
	if metrics.llmDurationMS == nil || startedAt.IsZero() {
		return
	}
	metrics.llmDurationMS.Record(ctx, durationMilliseconds(startedAt), metric.WithAttributes(
		attribute.String("component", sanitizeComponent(componentOrContext(ctx, component))),
		attribute.String("status", sanitizeStatus(status)),
	))
}

func EndSpan(span trace.Span, err error) {
	if err != nil {
		RecordSpanError(span, err)
	}
	span.End()
}

func isEnabled() bool {
	runtimeMu.RLock()
	defer runtimeMu.RUnlock()
	return runtimeEnabled
}

func setEnabled(enabled bool) {
	runtimeMu.Lock()
	defer runtimeMu.Unlock()
	runtimeEnabled = enabled
}

func normalizeConfig(cfg Config) Config {
	cfg.ServiceName = strings.TrimSpace(cfg.ServiceName)
	if cfg.ServiceName == "" {
		cfg.ServiceName = DefaultServiceName
	}
	cfg.ServiceVersion = strings.TrimSpace(cfg.ServiceVersion)
	cfg.Environment = strings.TrimSpace(cfg.Environment)
	cfg.OTLPEndpoint = strings.TrimSpace(cfg.OTLPEndpoint)
	cfg.TracesEndpoint = strings.TrimSpace(cfg.TracesEndpoint)
	cfg.MetricsEndpoint = strings.TrimSpace(cfg.MetricsEndpoint)
	if cfg.Enabled {
		if !cfg.TracesEnabled && !cfg.MetricsEnabled {
			cfg.TracesEnabled = cfg.OTLPEndpoint != "" || cfg.TracesEndpoint != ""
			cfg.MetricsEnabled = cfg.OTLPEndpoint != "" || cfg.MetricsEndpoint != ""
		}
		cfg.Enabled = cfg.TracesEnabled || cfg.MetricsEnabled
	}
	return cfg
}

func validateConfig(cfg Config) error {
	for name, value := range map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT":         cfg.OTLPEndpoint,
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT":  cfg.TracesEndpoint,
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": cfg.MetricsEndpoint,
	} {
		if err := validateEndpoint(name, value); err != nil {
			return err
		}
	}
	return nil
}

func validateEndpoint(name, value string) error {
	if value == "" {
		return nil
	}
	if strings.ContainsAny(value, " \t\r\n") {
		return fmt.Errorf("%s contains whitespace", name)
	}
	if !strings.Contains(value, "://") {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("%s is not a valid OTLP endpoint URL", name)
	}
	return nil
}

func resourceForConfig(ctx context.Context, cfg Config) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		attribute.String("service.name", cfg.ServiceName),
	}
	if cfg.ServiceVersion != "" {
		attrs = append(attrs, attribute.String("service.version", cfg.ServiceVersion))
	}
	if cfg.Environment != "" {
		attrs = append(attrs, attribute.String("deployment.environment", cfg.Environment))
	}
	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(attrs...),
	)
	if err != nil {
		return nil, fmt.Errorf("build telemetry resource: %w", err)
	}
	return res, nil
}

func traceExporterOptions(cfg Config) []otlptracegrpc.Option {
	endpoint := cfg.TracesEndpoint
	if endpoint == "" {
		endpoint = cfg.OTLPEndpoint
	}
	if endpoint == "" {
		return nil
	}
	if strings.Contains(endpoint, "://") {
		return []otlptracegrpc.Option{otlptracegrpc.WithEndpointURL(endpoint)}
	}
	return []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(endpoint)}
}

func metricExporterOptions(cfg Config) []otlpmetricgrpc.Option {
	endpoint := cfg.MetricsEndpoint
	if endpoint == "" {
		endpoint = cfg.OTLPEndpoint
	}
	if endpoint == "" {
		return nil
	}
	if strings.Contains(endpoint, "://") {
		return []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpointURL(endpoint)}
	}
	return []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpoint(endpoint)}
}

func ensureInstruments() metricInstruments {
	instrumentMu.Lock()
	defer instrumentMu.Unlock()
	if instruments.initialized {
		return instruments
	}

	meter := otel.Meter(instrumentationName)
	instruments.turnsStarted, _ = meter.Int64Counter("pyttechat_chat_turns_started_total")
	instruments.turnsCompleted, _ = meter.Int64Counter("pyttechat_chat_turns_completed_total")
	instruments.turnsCancelled, _ = meter.Int64Counter("pyttechat_chat_turns_cancelled_total")
	instruments.turnsFailed, _ = meter.Int64Counter("pyttechat_chat_turns_failed_total")
	instruments.streamEvents, _ = meter.Int64Counter("pyttechat_llm_stream_events_total")
	instruments.llmDurationMS, _ = meter.Float64Histogram("pyttechat_llm_request_duration_ms", metric.WithUnit("ms"))
	instruments.turnDurationMS, _ = meter.Float64Histogram("pyttechat_chat_turn_duration_ms", metric.WithUnit("ms"))
	instruments.initialized = true
	return instruments
}

func recordTurnDuration(ctx context.Context, metrics metricInstruments, startedAt time.Time, status string) {
	if metrics.turnDurationMS == nil || startedAt.IsZero() {
		return
	}
	metrics.turnDurationMS.Record(ctx, durationMilliseconds(startedAt), metric.WithAttributes(
		componentAttribute(ctx),
		attribute.String("status", sanitizeStatus(status)),
	))
}

func durationMilliseconds(startedAt time.Time) float64 {
	return float64(time.Since(startedAt).Nanoseconds()) / float64(time.Millisecond)
}

func componentAttribute(ctx context.Context) attribute.KeyValue {
	return attribute.String("component", componentOrContext(ctx, ""))
}

func componentOrContext(ctx context.Context, component string) string {
	if component == "" {
		if value, ok := ctx.Value(componentContextKey).(string); ok {
			component = value
		}
	}
	return sanitizeComponent(component)
}

func sanitizeComponent(component string) string {
	switch strings.TrimSpace(component) {
	case ComponentCLI:
		return ComponentCLI
	case ComponentWeb:
		return ComponentWeb
	case ComponentLLMProxy:
		return ComponentLLMProxy
	case ComponentFakeProvider:
		return ComponentFakeProvider
	default:
		return "unknown"
	}
}

func sanitizeStatus(status string) string {
	switch strings.TrimSpace(status) {
	case ChatTurnStatusCompleted:
		return ChatTurnStatusCompleted
	case ChatTurnStatusCancelled:
		return ChatTurnStatusCancelled
	case ChatTurnStatusFailed:
		return ChatTurnStatusFailed
	default:
		return "unknown"
	}
}

func sanitizeEventType(eventType string) string {
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		return "unknown"
	}
	for _, r := range eventType {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '.' || r == '-' {
			continue
		}
		return "unknown"
	}
	if len(eventType) > 80 {
		return "unknown"
	}
	return eventType
}

func errorType(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "context_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "context_deadline_exceeded"
	}
	errType := reflect.TypeOf(err)
	if errType == nil {
		return "unknown"
	}
	return strings.TrimPrefix(errType.String(), "*")
}

func envString(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func sdkDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OTEL_SDK_DISABLED"))) {
	case "1", "true", "t", "yes", "y", "on":
		return true
	default:
		return false
	}
}

type globalSnapshot struct {
	tracerProvider trace.TracerProvider
	meterProvider  metric.MeterProvider
	propagator     propagation.TextMapPropagator
	enabled        bool
}

func captureGlobals() globalSnapshot {
	runtimeMu.RLock()
	enabled := runtimeEnabled
	runtimeMu.RUnlock()
	return globalSnapshot{
		tracerProvider: otel.GetTracerProvider(),
		meterProvider:  otel.GetMeterProvider(),
		propagator:     otel.GetTextMapPropagator(),
		enabled:        enabled,
	}
}

func (s globalSnapshot) restore() {
	otel.SetTracerProvider(s.tracerProvider)
	otel.SetMeterProvider(s.meterProvider)
	otel.SetTextMapPropagator(s.propagator)
	setEnabled(s.enabled)
}

func onceShutdown(shutdown ShutdownFunc) ShutdownFunc {
	var once sync.Once
	var err error
	return func(ctx context.Context) error {
		once.Do(func() {
			err = shutdown(ctx)
		})
		return err
	}
}
