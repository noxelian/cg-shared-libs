package ws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtractToken(t *testing.T) {
	tests := []struct {
		name      string
		header    string
		protocols string
		query     string
		want      string
	}{
		{name: "authorization", header: "Bearer header-token", want: "header-token"},
		{name: "authorization scheme is case insensitive", header: "bearer header-token", want: "header-token"},
		{name: "websocket subprotocol", protocols: "access_token, protocol-token", want: "protocol-token"},
		{name: "websocket subprotocol pair must be exact", protocols: "graphql-ws, access_token, protocol-token", want: ""},
		{name: "websocket subprotocol pair rejects suffix", protocols: "access_token, protocol-token, graphql-ws", want: ""},
		{name: "missing websocket token falls back to authorization", header: "Bearer header-token", protocols: "access_token", want: "header-token"},
		{name: "query token rejected", query: "query-token", want: ""},
		{name: "unrelated subprotocol rejected", protocols: "graphql-ws", want: ""},
		{name: "malformed authorization rejected", header: "Bearer one two", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), "GET", "/ws?token="+tt.query, http.NoBody)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			if tt.protocols != "" {
				req.Header.Set("Sec-WebSocket-Protocol", tt.protocols)
			}

			if got := ExtractToken(req); got != tt.want {
				t.Fatalf("ExtractToken() = %q, want %q", got, tt.want)
			}
		})
	}
}
