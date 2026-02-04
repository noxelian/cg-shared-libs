package ws

import "time"

// Config holds WebSocket server configuration
type Config struct {
	Host            string        `yaml:"host" env:"WS_HOST" env-default:"0.0.0.0"`
	Port            int           `yaml:"port" env:"WS_PORT" env-default:"8081"`
	ReadBufferSize  int           `yaml:"read_buffer_size" env:"WS_READ_BUFFER_SIZE" env-default:"1024"`
	WriteBufferSize int           `yaml:"write_buffer_size" env:"WS_WRITE_BUFFER_SIZE" env-default:"1024"`
	PingPeriod      time.Duration `yaml:"ping_period" env:"WS_PING_PERIOD" env-default:"30s"`
	PongWait        time.Duration `yaml:"pong_wait" env:"WS_PONG_WAIT" env-default:"60s"`
	WriteWait       time.Duration `yaml:"write_wait" env:"WS_WRITE_WAIT" env-default:"10s"`
	MaxMessageSize  int64         `yaml:"max_message_size" env:"WS_MAX_MESSAGE_SIZE" env-default:"524288"` // 512KB
}

// DefaultConfig returns default WebSocket configuration
func DefaultConfig() Config {
	return Config{
		Host:            "0.0.0.0",
		Port:            8081,
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		PingPeriod:      30 * time.Second,
		PongWait:        60 * time.Second,
		WriteWait:       10 * time.Second,
		MaxMessageSize:  512 * 1024, // 512KB
	}
}
