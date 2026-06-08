package observability

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"example.com/llm-chat-web/internal/buildinfo"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestFromEnvDefaultsAndEndpointEnablement(t *testing.T) {
	clearTelemetryEnv(t)

	cfg := FromEnv(buildinfo.Info{Version: "1.2.3"})
	if cfg.Enabled {
		t.Fatalf("Enabled = true, want false without OTLP endpoints")
	}
	if cfg.ServiceName != "pyttechat" {
		t.Fatalf("ServiceName = %q, want pyttechat", cfg.ServiceName)
	}
	if cfg.ServiceVersion != "1.2.3" {
		t.Fatalf("ServiceVersion = %q, want build version", cfg.ServiceVersion)
	}

	t.Setenv("OTEL_SERVICE_NAME", "custom-service")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "http://collector.example:4317")
	cfg = FromEnv(buildinfo.Info{Version: "dev"})
	if !cfg.Enabled || !cfg.TracesEnabled || cfg.MetricsEnabled {
		t.Fatalf("cfg = %#v, want traces enabled only", cfg)
	}
	if cfg.ServiceName != "custom-service" {
		t.Fatalf("ServiceName = %q, want custom-service", cfg.ServiceName)
	}

	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "http://collector.example:4317")
	cfg = FromEnv(buildinfo.Info{Version: "dev"})
	if !cfg.Enabled || !cfg.TracesEnabled || !cfg.MetricsEnabled {
		t.Fatalf("cfg = %#v, want traces and metrics enabled", cfg)
	}

	t.Setenv("OTEL_SDK_DISABLED", "true")
	cfg = FromEnv(buildinfo.Info{Version: "dev"})
	if cfg.Enabled || cfg.TracesEnabled || cfg.MetricsEnabled {
		t.Fatalf("cfg = %#v, want SDK disabled to force telemetry off", cfg)
	}
}

func TestInitDisabledDefaultIsNoopAndShutdownSafe(t *testing.T) {
	clearTelemetryEnv(t)
	restore := captureGlobals().restore
	t.Cleanup(restore)

	shutdown, err := Init(context.Background(), FromEnv(buildinfo.Info{}))
	if err != nil {
		t.Fatalf("Init disabled error = %v, want nil", err)
	}
	if isEnabled() {
		t.Fatalf("telemetry runtime is enabled, want disabled")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("first shutdown error = %v, want nil", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("second shutdown error = %v, want nil", err)
	}
}

func TestInitEnabledSetsProvidersAndRestoresGlobals(t *testing.T) {
	clearTelemetryEnv(t)
	restore := captureGlobals().restore
	t.Cleanup(restore)

	cfg := Config{
		ServiceName:    "test-service",
		ServiceVersion: "test-version",
		Environment:    "test",
		OTLPEndpoint:   "http://127.0.0.1:4317",
		Enabled:        true,
		TracesEnabled:  true,
	}
	shutdown, err := Init(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Init enabled error = %v, want nil", err)
	}
	if !isEnabled() {
		t.Fatalf("telemetry runtime is disabled, want enabled")
	}
	if otel.GetTextMapPropagator().Fields()[0] != "traceparent" {
		t.Fatalf("propagator fields = %#v, want tracecontext propagator", otel.GetTextMapPropagator().Fields())
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown error = %v, want nil", err)
	}
	if isEnabled() {
		t.Fatalf("telemetry runtime is enabled after shutdown, want restored disabled state")
	}
}

func TestInitEnabledRejectsInvalidEndpoint(t *testing.T) {
	clearTelemetryEnv(t)
	restore := captureGlobals().restore
	t.Cleanup(restore)

	_, err := Init(context.Background(), Config{
		OTLPEndpoint:   "http://",
		Enabled:        true,
		TracesEnabled:  true,
		MetricsEnabled: true,
	})
	if err == nil || !strings.Contains(err.Error(), "OTLP endpoint") {
		t.Fatalf("Init invalid endpoint error = %v, want OTLP endpoint validation error", err)
	}
	if isEnabled() {
		t.Fatalf("telemetry runtime is enabled after failed init, want disabled")
	}
}

func TestHTTPMiddlewarePreservesBehaviorAndNormalizesRouteName(t *testing.T) {
	recorder := installSpanRecorder(t)
	setEnabledForTest(t, true)

	handler := HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Test", "ok")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("body"))
	}))

	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/chat/turns/turn_secret/events", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted || response.Header().Get("X-Test") != "ok" || response.Body.String() != "body" {
		t.Fatalf("middleware response = status %d header %q body %q, want handler behavior preserved", response.Code, response.Header().Get("X-Test"), response.Body.String())
	}
	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended span count = %d, want 1", len(ended))
	}
	if got := ended[0].Name(); got != "GET /chat/turns/{turn_id}/events" {
		t.Fatalf("span name = %q, want normalized turn events route", got)
	}
}

func TestHTTPMiddlewareDisabledReturnsOriginalHandler(t *testing.T) {
	setEnabledForTest(t, false)

	handler := &comparableHandler{}
	if got := HTTPMiddleware(handler); got != handler {
		t.Fatalf("disabled middleware returned %T, want original handler", got)
	}
}

func TestHTTPClientTransportDisabledReturnsBaseAndEnabledPropagatesTraceContext(t *testing.T) {
	restore := captureGlobals().restore
	t.Cleanup(restore)

	base := http.DefaultTransport
	setEnabledForTest(t, false)
	if got := HTTPClientTransport(base); got != base {
		t.Fatalf("disabled transport = %T, want original base transport", got)
	}

	installSpanRecorder(t)
	setEnabledForTest(t, true)

	var traceparent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceparent = r.Header.Get("traceparent")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := &http.Client{Transport: HTTPClientTransport(http.DefaultTransport)}
	ctx, span := otel.Tracer(instrumentationName).Start(context.Background(), "parent")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest error = %v", err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("client.Do error = %v", err)
	}
	response.Body.Close()
	span.End()

	if traceparent == "" {
		t.Fatalf("traceparent header is empty, want propagated trace context")
	}
}

func TestRouteNameNormalizesKnownRoutes(t *testing.T) {
	for _, tc := range []struct {
		method string
		path   string
		want   string
	}{
		{method: "get", path: "", want: "GET /"},
		{method: "GET", path: "/assets/app.js", want: "GET /assets/*"},
		{method: "GET", path: "/favicon.ico", want: "GET /favicon.ico"},
		{method: "POST", path: "/login", want: "POST /login"},
		{method: "GET", path: "/register", want: "GET /register"},
		{method: "POST", path: "/logout", want: "POST /logout"},
		{method: "POST", path: "/chat/turns", want: "POST /chat/turns"},
		{method: "GET", path: "/chat/turns/secret/events", want: "GET /chat/turns/{turn_id}/events"},
		{method: "POST", path: "/chat/turns/secret/abort", want: "POST /chat/turns/{turn_id}/abort"},
		{method: "GET", path: "/chat/turns/secret/unknown", want: "GET unmatched"},
		{method: "GET", path: "/users/secret", want: "GET unmatched"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			if got := RouteName(tc.method, tc.path); got != tc.want {
				t.Fatalf("RouteName(%q, %q) = %q, want %q", tc.method, tc.path, got, tc.want)
			}
		})
	}
}

func TestManualSpansAndMetricsHelpersAreSafeWithNoopProviders(t *testing.T) {
	recorder := installSpanRecorder(t)
	setEnabledForTest(t, true)
	resetInstruments(t)

	ctx := ContextWithComponent(context.Background(), ComponentWeb)
	ctx, span := StartSpan(ctx, "test.span", "", attribute.String("custom", "bounded"))
	RecordSpanError(span, context.Canceled)
	SetSpanStatus(span, ChatTurnStatusCompleted)

	startedAt := time.Now().Add(-2 * time.Millisecond)
	ChatTurnStarted(ctx)
	ChatTurnCompleted(ctx, startedAt)
	ChatTurnCancelled(ctx, startedAt)
	ChatTurnFailed(ctx, startedAt, context.DeadlineExceeded)
	LLMStreamEvent(ctx, ComponentWeb, "response.completed")
	RecordLLMRequestDuration(ctx, ComponentLLMProxy, startedAt, ChatTurnStatusCompleted)
	EndSpan(span, nil)

	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended span count = %d, want 1", len(ended))
	}
	if ended[0].Name() != "test.span" {
		t.Fatalf("span name = %q, want test.span", ended[0].Name())
	}

	// Repeated calls reuse already-created instruments.
	ChatTurnStarted(ctx)
	LLMStreamEvent(ctx, ComponentWeb, "response.completed")
}

func TestSanitizersAndExporterOptions(t *testing.T) {
	if got := sanitizeComponent("bad"); got != "unknown" {
		t.Fatalf("sanitizeComponent = %q, want unknown", got)
	}
	if got := sanitizeStatus("bad"); got != "unknown" {
		t.Fatalf("sanitizeStatus = %q, want unknown", got)
	}
	if got := sanitizeEventType("response.completed"); got != "response.completed" {
		t.Fatalf("sanitizeEventType valid = %q, want response.completed", got)
	}
	if got := sanitizeEventType("bad event"); got != "unknown" {
		t.Fatalf("sanitizeEventType invalid = %q, want unknown", got)
	}
	if got := sanitizeEventType(strings.Repeat("a", 81)); got != "unknown" {
		t.Fatalf("sanitizeEventType long = %q, want unknown", got)
	}
	if got := errorType(context.Canceled); got != "context_canceled" {
		t.Fatalf("errorType canceled = %q, want context_canceled", got)
	}
	if got := errorType(context.DeadlineExceeded); got != "context_deadline_exceeded" {
		t.Fatalf("errorType deadline = %q, want context_deadline_exceeded", got)
	}
	if got := errorType(errors.New("plain")); got != "errors.errorString" {
		t.Fatalf("errorType plain = %q, want errors.errorString", got)
	}

	cfg := normalizeConfig(Config{Enabled: true, OTLPEndpoint: "collector:4317"})
	if !cfg.Enabled || !cfg.TracesEnabled || !cfg.MetricsEnabled {
		t.Fatalf("normalizeConfig shared endpoint = %#v, want both signals enabled", cfg)
	}
	cfg = normalizeConfig(Config{Enabled: true})
	if cfg.Enabled {
		t.Fatalf("normalizeConfig no endpoints = %#v, want disabled", cfg)
	}

	if err := validateEndpoint("test", "bad endpoint"); err == nil {
		t.Fatalf("validateEndpoint whitespace error = nil, want error")
	}
	if err := validateEndpoint("test", "http://"); err == nil {
		t.Fatalf("validateEndpoint bad URL error = nil, want error")
	}
	if err := validateEndpoint("test", "collector:4317"); err != nil {
		t.Fatalf("validateEndpoint host:port error = %v, want nil", err)
	}
	if err := validateConfig(Config{OTLPEndpoint: "collector:4317"}); err != nil {
		t.Fatalf("validateConfig error = %v, want nil", err)
	}

	if len(traceExporterOptions(Config{OTLPEndpoint: "collector:4317"})) != 1 {
		t.Fatalf("traceExporterOptions host:port length != 1")
	}
	if len(traceExporterOptions(Config{TracesEndpoint: "http://collector:4317"})) != 1 {
		t.Fatalf("traceExporterOptions URL length != 1")
	}
	if len(metricExporterOptions(Config{OTLPEndpoint: "collector:4317"})) != 1 {
		t.Fatalf("metricExporterOptions host:port length != 1")
	}
	if len(metricExporterOptions(Config{MetricsEndpoint: "http://collector:4317"})) != 1 {
		t.Fatalf("metricExporterOptions URL length != 1")
	}
	if len(traceExporterOptions(Config{})) != 0 || len(metricExporterOptions(Config{})) != 0 {
		t.Fatalf("empty exporter options should be empty")
	}
}

func TestResourceForConfigIncludesConfiguredAttributes(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment=local,service.namespace=test")
	res, err := resourceForConfig(context.Background(), Config{
		ServiceName:    "svc",
		ServiceVersion: "v1",
		Environment:    "override",
	})
	if err != nil {
		t.Fatalf("resourceForConfig error = %v, want nil", err)
	}
	attrs := res.Set()
	for _, want := range []attribute.Key{
		"service.name",
		"service.version",
		"deployment.environment",
		"service.namespace",
	} {
		if _, ok := attrs.Value(want); !ok {
			t.Fatalf("resource missing attribute %q", want)
		}
	}
}

func installSpanRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()

	restore := captureGlobals().restore
	t.Cleanup(restore)

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	return recorder
}

func clearTelemetryEnv(t *testing.T) {
	t.Helper()

	for _, name := range []string{
		"OTEL_SERVICE_NAME",
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
		"OTEL_RESOURCE_ATTRIBUTES",
		"OTEL_SDK_DISABLED",
	} {
		t.Setenv(name, "")
	}
}

func setEnabledForTest(t *testing.T, enabled bool) {
	t.Helper()

	runtimeMu.Lock()
	previous := runtimeEnabled
	runtimeEnabled = enabled
	runtimeMu.Unlock()

	t.Cleanup(func() {
		runtimeMu.Lock()
		runtimeEnabled = previous
		runtimeMu.Unlock()
	})
}

func resetInstruments(t *testing.T) {
	t.Helper()

	instrumentMu.Lock()
	previous := instruments
	instruments = metricInstruments{}
	instrumentMu.Unlock()

	t.Cleanup(func() {
		instrumentMu.Lock()
		instruments = previous
		instrumentMu.Unlock()
	})
}

type comparableHandler struct{}

func (h *comparableHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}
