package config

import "fmt"

// HTTPConfig holds HTTP server configuration
type HTTPConfig struct {
	Host string `yaml:"host" env:"HTTP_HOST" env-default:"0.0.0.0"`
	Port int    `yaml:"port" env:"HTTP_PORT" env-default:"8080"`
}

// Address returns the HTTP server address in host:port format
func (c HTTPConfig) Address() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}
