package ws

import (
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"gitlab.com/xakpro/cg-shared-libs/logger"
	"go.uber.org/zap"
)

var (
	// allowedOrigins contains list of allowed WebSocket origins
	allowedOrigins = []string{
		"https://ctogram.kz",
		"https://app.ctogram.kz",
	}
	originsMu sync.RWMutex
)

// SetAllowedOrigins updates the allowed origins list
func SetAllowedOrigins(origins []string) {
	originsMu.Lock()
	defer originsMu.Unlock()
	allowedOrigins = origins
}

// GetAllowedOrigins returns current allowed origins
func GetAllowedOrigins() []string {
	originsMu.RLock()
	defer originsMu.RUnlock()
	result := make([]string, len(allowedOrigins))
	copy(result, allowedOrigins)
	return result
}

// CheckOrigin validates WebSocket connection origin
func CheckOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Allow requests without Origin header (e.g., mobile apps)
		return true
	}

	originsMu.RLock()
	defer originsMu.RUnlock()

	for _, allowed := range allowedOrigins {
		if origin == allowed {
			return true
		}
	}

	logger.Warn("websocket: rejected connection from disallowed origin",
		zap.String("origin", origin),
		zap.String("remote_addr", r.RemoteAddr),
	)
	return false
}

// NewUpgrader creates a new WebSocket upgrader with default settings
func NewUpgrader(cfg Config) *websocket.Upgrader {
	readBufSize := cfg.ReadBufferSize
	if readBufSize == 0 {
		readBufSize = 1024
	}
	writeBufSize := cfg.WriteBufferSize
	if writeBufSize == 0 {
		writeBufSize = 1024
	}

	return &websocket.Upgrader{
		ReadBufferSize:  readBufSize,
		WriteBufferSize: writeBufSize,
		CheckOrigin:     CheckOrigin,
	}
}

// DefaultUpgrader returns an upgrader with default configuration
func DefaultUpgrader() *websocket.Upgrader {
	return NewUpgrader(DefaultConfig())
}
