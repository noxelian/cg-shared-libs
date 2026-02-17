package grpc

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"google.golang.org/grpc/credentials"
)

// TLSConfig holds TLS configuration for gRPC server/client.
// When Enabled is false, insecure credentials are used (development mode).
type TLSConfig struct {
	Enabled  bool   `yaml:"enabled" env:"GRPC_TLS_ENABLED" env-default:"false"`
	CertFile string `yaml:"cert_file" env:"GRPC_TLS_CERT_FILE"`
	KeyFile  string `yaml:"key_file" env:"GRPC_TLS_KEY_FILE"`
	CAFile   string `yaml:"ca_file" env:"GRPC_TLS_CA_FILE"`
}

// ServerCredentials returns gRPC transport credentials for the server.
// Returns nil if TLS is disabled.
func (c *TLSConfig) ServerCredentials() (credentials.TransportCredentials, error) {
	if !c.Enabled {
		return nil, nil
	}

	cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load server key pair: %w", err)
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	// If CA file is provided, use mutual TLS (mTLS)
	if c.CAFile != "" {
		caCert, err := os.ReadFile(c.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file: %w", err)
		}

		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to append CA certs")
		}

		tlsCfg.ClientCAs = pool
		tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
	}

	return credentials.NewTLS(tlsCfg), nil
}

// ClientCredentials returns gRPC transport credentials for the client.
// Returns nil if TLS is disabled.
func (c *TLSConfig) ClientCredentials() (credentials.TransportCredentials, error) {
	if !c.Enabled {
		return nil, nil
	}

	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	// Load CA certificate for server verification
	if c.CAFile != "" {
		caCert, err := os.ReadFile(c.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file: %w", err)
		}

		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to append CA certs")
		}

		tlsCfg.RootCAs = pool
	}

	// Load client cert/key for mTLS
	if c.CertFile != "" && c.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load client key pair: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	return credentials.NewTLS(tlsCfg), nil
}
