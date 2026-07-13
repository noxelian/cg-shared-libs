package kafka

import (
	"errors"
	"fmt"
	"testing"
	"time"

	segmentio "github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewWriterRequiresAllInSyncReplicas(t *testing.T) {
	t.Parallel()

	writer := newWriter(Config{
		Brokers:      []string{"kafka-1:9092", "kafka-2:9092"},
		BatchSize:    25,
		BatchTimeout: 250 * time.Millisecond,
	}, "request.events")

	assert.Equal(t, segmentio.RequireAll, writer.RequiredAcks)
	assert.False(t, writer.Async)
	assert.Equal(t, "request.events", writer.Topic)
	assert.Equal(t, 25, writer.BatchSize)
	assert.Equal(t, 250*time.Millisecond, writer.BatchTimeout)
}

func TestExtraWriterPreservesAcknowledgementGuarantee(t *testing.T) {
	t.Parallel()

	producer := NewProducer(Config{Brokers: []string{"kafka:9092"}}, "request.events")
	extra := producer.writerFor("notification.push")

	assert.Equal(t, segmentio.RequireAll, extra.RequiredAcks)
	assert.False(t, extra.Async)
	assert.Equal(t, "notification.push", extra.Topic)
}

// --- UnmarshalError ---

func TestNewUnmarshalError_Error(t *testing.T) {
	cause := errors.New("unexpected end of JSON input")
	ue := NewUnmarshalError(cause)

	assert.Contains(t, ue.Error(), "unmarshal error")
	assert.Contains(t, ue.Error(), cause.Error())
}

func TestUnmarshalError_Unwrap(t *testing.T) {
	cause := errors.New("bad token")
	ue := NewUnmarshalError(cause)

	assert.True(t, errors.Is(ue, cause))
}

func TestIsUnmarshalError_DirectMatch(t *testing.T) {
	ue := NewUnmarshalError(errors.New("some decode error"))

	assert.True(t, IsUnmarshalError(ue))
}

func TestIsUnmarshalError_Wrapped(t *testing.T) {
	cause := NewUnmarshalError(errors.New("bad json"))
	wrapped := fmt.Errorf("handler: %w", cause)

	assert.True(t, IsUnmarshalError(wrapped))
}

func TestIsUnmarshalError_OtherError(t *testing.T) {
	other := errors.New("connection refused")

	assert.False(t, IsUnmarshalError(other))
}

func TestIsUnmarshalError_Nil(t *testing.T) {
	assert.False(t, IsUnmarshalError(nil))
}

// --- FlexibleTime ---

func TestFlexibleTime_RFC3339(t *testing.T) {
	var ft FlexibleTime
	err := ft.UnmarshalJSON([]byte(`"2024-01-15T10:30:00Z"`))

	require.NoError(t, err)
	assert.Equal(t, 2024, ft.Year())
	assert.Equal(t, 1, int(ft.Month()))
	assert.Equal(t, 15, ft.Day())
}

func TestFlexibleTime_RFC3339Nano(t *testing.T) {
	var ft FlexibleTime
	err := ft.UnmarshalJSON([]byte(`"2024-01-15T10:30:00.123456789Z"`))

	require.NoError(t, err)
	assert.Equal(t, 2024, ft.Year())
}

func TestFlexibleTime_UnixTimestampNumber(t *testing.T) {
	var ft FlexibleTime
	err := ft.UnmarshalJSON([]byte(`1705312200`))

	require.NoError(t, err)
	assert.Equal(t, int64(1705312200), ft.Unix())
}

func TestFlexibleTime_UnixTimestampString(t *testing.T) {
	var ft FlexibleTime
	err := ft.UnmarshalJSON([]byte(`"1705312200"`))

	require.NoError(t, err)
	assert.Equal(t, int64(1705312200), ft.Unix())
}

func TestFlexibleTime_InvalidValue(t *testing.T) {
	var ft FlexibleTime
	err := ft.UnmarshalJSON([]byte(`true`))

	assert.Error(t, err)
}

func TestFlexibleTime_InvalidStringFormat(t *testing.T) {
	var ft FlexibleTime
	err := ft.UnmarshalJSON([]byte(`"not-a-date"`))

	assert.Error(t, err)
}

func TestFlexibleTime_MarshalJSON(t *testing.T) {
	var ft FlexibleTime
	require.NoError(t, ft.UnmarshalJSON([]byte(`"2024-06-01T00:00:00Z"`)))

	data, err := ft.MarshalJSON()

	require.NoError(t, err)
	assert.Contains(t, string(data), "2024-06-01")
}
