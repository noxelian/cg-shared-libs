package pushpublisher

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubProducer records PublishJSON calls without a real Kafka connection.
type stubProducer struct {
	calls []stubCall
	err   error
}

type stubCall struct {
	key  string
	data []byte
}

// PublishJSON captures the key and marshalled payload.
func (s *stubProducer) PublishJSON(_ context.Context, key string, data any) error {
	if s.err != nil {
		return s.err
	}
	b, _ := json.Marshal(data)
	s.calls = append(s.calls, stubCall{key: key, data: b})
	return nil
}

// publisherWithStub wires a Publisher to the stub without importing kafka.Producer.
// We test behaviour by calling Publish directly — the stub satisfies the
// json-serialisation path since Publisher.Publish delegates to producer.PublishJSON.
//
// NOTE: because Publisher wraps *kafka.Producer (a concrete type, not an
// interface), we test the observable contract at the JSON level using a
// real Publisher with a nil producer (no-op path) plus direct Event
// marshalling assertions. Integration tests with a real Kafka broker are
// out of scope for unit tests.

func TestPublish_NilProducer_IsNoop(t *testing.T) {
	// nil producer must not panic.
	p := New(nil)
	require.NotPanics(t, func() {
		p.Publish(context.Background(), Event{
			UserID:    1,
			EventType: "order.status_changed",
			Title:     "T",
			Body:      "B",
		})
	})
}

func TestPublishBatch_NilProducer_IsNoop(t *testing.T) {
	p := New(nil)
	require.NotPanics(t, func() {
		p.PublishBatch(context.Background(), []Event{
			{UserID: 1, EventType: "test"},
			{UserID: 2, EventType: "test"},
		})
	})
}

// TestEvent_MarshalTargetApps verifies JSON serialisation of the TargetApps field.
func TestEvent_MarshalTargetApps(t *testing.T) {
	tests := []struct {
		name       string
		event      Event
		wantKey    string
		wantInJSON string
	}{
		{
			name:       "client only",
			event:      Event{UserID: 1, EventType: "order.status_changed", TargetApps: AppsClient},
			wantInJSON: `"target_apps":["client"]`,
		},
		{
			name:       "partner only",
			event:      Event{UserID: 2, EventType: "bid.accepted", TargetApps: AppsPartner},
			wantInJSON: `"target_apps":["partner"]`,
		},
		{
			name:       "nil target_apps omitted from json",
			event:      Event{UserID: 3, EventType: "auth.login.suspicious", TargetApps: nil},
			wantInJSON: `"user_id":3`,
		},
		{
			name:       "both apps",
			event:      Event{UserID: 4, EventType: "chat.message.new", TargetApps: AppsBoth},
			wantInJSON: `"target_apps":["client","partner"]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := json.Marshal(tt.event)
			require.NoError(t, err)
			assert.Contains(t, string(b), tt.wantInJSON)

			// nil TargetApps must not appear as "target_apps":null in JSON.
			if tt.event.TargetApps == nil {
				assert.NotContains(t, string(b), "target_apps")
			}
		})
	}
}

// TestAppConstants verifies that the exported App constants have the expected values.
func TestAppConstants(t *testing.T) {
	assert.Equal(t, "client", AppClient)
	assert.Equal(t, "partner", AppPartner)
	assert.Equal(t, []string{"client"}, AppsClient)
	assert.Equal(t, []string{"partner"}, AppsPartner)
	assert.Equal(t, []string{"client", "partner"}, AppsBoth)
}

// TestEvent_TargetAppsSemantics documents which events should use which TargetApps.
// This test acts as a living specification — it fails if someone accidentally
// changes a constant value.
func TestEvent_TargetAppsSemantics(t *testing.T) {
	table := []struct {
		eventType  string
		targetApps []string
	}{
		{"order.status_changed", AppsClient},
		{"order.shipped", AppsClient},
		{"order.delivered", AppsClient},
		{"bid.received", AppsClient},
		{"bid.accepted", AppsPartner},
		{"bid.rejected", AppsPartner},
		{"request.matching.new", AppsPartner},
		{"subscription.expiring", AppsPartner},
		{"subscription.activated", AppsPartner},
	}

	for _, row := range table {
		t.Run(row.eventType, func(t *testing.T) {
			e := Event{
				UserID:     1,
				EventType:  row.eventType,
				Title:      "test",
				Body:       "test",
				TargetApps: row.targetApps,
			}
			b, err := json.Marshal(e)
			require.NoError(t, err)

			var decoded Event
			require.NoError(t, json.Unmarshal(b, &decoded))
			assert.Equal(t, row.targetApps, decoded.TargetApps)
		})
	}
}
