package logger

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestMaskPhone(t *testing.T) {
	tests := []struct {
		phone    string
		expected string
	}{
		{"+77001234567", "+7***4567"},
		{"+79001234567", "+7***4567"},
		{"1234", "****"},
		{"123", "****"},
		{"", "****"},
		{"+7700", "+7***7700"},
	}

	for _, tt := range tests {
		result := MaskPhone(tt.phone)
		if result != tt.expected {
			t.Errorf("MaskPhone(%q) = %q, want %q", tt.phone, result, tt.expected)
		}
	}
}

func TestWithTraceID_NoSpan(t *testing.T) {
	_ = Init(Config{Level: "info", Encoding: "json"})

	ctx := context.Background()
	newCtx := WithTraceID(ctx)

	// Without a span, context should be returned unchanged (no logger enrichment crash)
	if newCtx == nil {
		t.Fatal("WithTraceID returned nil context")
	}
}

func TestWithTraceID_WithSpan(t *testing.T) {
	_ = Init(Config{Level: "info", Encoding: "json"})

	// Create a context with a valid span context
	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	newCtx := WithTraceID(ctx)
	if newCtx == nil {
		t.Fatal("WithTraceID returned nil context")
	}

	// Verify logger was enriched (it's stored in context)
	l := WithContext(newCtx)
	if l == nil {
		t.Fatal("WithContext returned nil logger after WithTraceID")
	}
}
