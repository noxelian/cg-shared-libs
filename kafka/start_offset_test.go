package kafka

import (
	"testing"

	kafkago "github.com/segmentio/kafka-go"
)

func TestConsumerStartOffset(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  int64
		valid bool
	}{
		{name: "default", value: "", want: kafkago.FirstOffset, valid: true},
		{name: "earliest", value: "earliest", want: kafkago.FirstOffset, valid: true},
		{name: "first alias", value: "FIRST", want: kafkago.FirstOffset, valid: true},
		{name: "latest", value: "latest", want: kafkago.LastOffset, valid: true},
		{name: "last alias", value: " last ", want: kafkago.LastOffset, valid: true},
		{name: "invalid", value: "middle", want: kafkago.FirstOffset, valid: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, valid := consumerStartOffset(tt.value)
			if got != tt.want || valid != tt.valid {
				t.Fatalf("consumerStartOffset(%q) = (%d, %t), want (%d, %t)", tt.value, got, valid, tt.want, tt.valid)
			}
		})
	}
}
