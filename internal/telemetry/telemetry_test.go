package telemetry

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestNewFallsBackToCanonicalJSONWithoutOTLP(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	var output bytes.Buffer
	setup, err := New(context.Background(), "test-service", &output)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	setup.Logger.Error("failed", "camelCase", 1, "error", errors.New("secret detail"), "access_token", "hidden")
	line := output.String()
	for _, expected := range []string{`"camel_case":1`, `"error_type":"error_string"`} {
		if !strings.Contains(line, expected) {
			t.Fatalf("log %q does not contain %q", line, expected)
		}
	}
	if strings.Contains(line, "secret detail") || strings.Contains(line, "hidden") {
		t.Fatalf("log contains sensitive value: %q", line)
	}
	if err := setup.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestFanoutReturnsJoinedErrors(t *testing.T) {
	wantA := errors.New("a")
	wantB := errors.New("b")
	handler := newFanoutHandler(failingHandler{err: wantA}, failingHandler{err: wantB})
	record := slog.NewRecord(testTime(), slog.LevelInfo, "event", 0)
	err := handler.Handle(context.Background(), record)
	if !errors.Is(err, wantA) || !errors.Is(err, wantB) {
		t.Fatalf("Handle() error = %v", err)
	}
	_ = handler.Enabled(context.Background(), slog.LevelInfo)
	_ = handler.WithAttrs([]slog.Attr{slog.String("key", "value")})
	_ = handler.WithGroup("group")
}

type failingHandler struct{ err error }

func (handler failingHandler) Enabled(context.Context, slog.Level) bool  { return true }
func (handler failingHandler) Handle(context.Context, slog.Record) error { return handler.err }
func (handler failingHandler) WithAttrs([]slog.Attr) slog.Handler        { return handler }
func (handler failingHandler) WithGroup(string) slog.Handler             { return handler }

func testTime() time.Time { return time.Time{} }
