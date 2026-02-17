package jwt

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Config holds JWT configuration
type Config struct {
	SecretKey       string        `yaml:"secret_key" env:"JWT_SECRET_KEY"`
	AccessTokenTTL  time.Duration `yaml:"access_token_ttl" env:"JWT_ACCESS_TOKEN_TTL" env-default:"15m"`
	RefreshTokenTTL time.Duration `yaml:"refresh_token_ttl" env:"JWT_REFRESH_TOKEN_TTL" env-default:"720h"` // 30 days
	Issuer          string        `yaml:"issuer" env:"JWT_ISSUER" env-default:"cg-platform"`
}

// TokenType represents the type of JWT token
type TokenType string

const (
	TokenTypeAccess  TokenType = "access"
	TokenTypeRefresh TokenType = "refresh"
)

// Claims represents JWT claims
type Claims struct {
	UserID    int64     `json:"user_id"`
	Phone     string    `json:"phone,omitempty"`
	DeviceID  string    `json:"device_id,omitempty"`
	TokenType TokenType `json:"token_type,omitempty"`
	jwt.RegisteredClaims
}

// TokenPair contains access and refresh tokens
type TokenPair struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// Manager handles JWT operations
type Manager struct {
	secretKey       []byte
	accessTokenTTL  time.Duration
	refreshTokenTTL time.Duration
	issuer          string
}

// NewManager creates a new JWT manager
func NewManager(cfg Config) (*Manager, error) {
	if cfg.SecretKey == "" {
		return nil, errors.New("jwt secret key is required")
	}

	return &Manager{
		secretKey:       []byte(cfg.SecretKey),
		accessTokenTTL:  cfg.AccessTokenTTL,
		refreshTokenTTL: cfg.RefreshTokenTTL,
		issuer:          cfg.Issuer,
	}, nil
}

// GenerateTokenPair generates access and refresh tokens
func (m *Manager) GenerateTokenPair(userID int64, phone, deviceID string) (*TokenPair, error) {
	accessToken, expiresAt, err := m.generateToken(userID, phone, deviceID, m.accessTokenTTL, TokenTypeAccess)
	if err != nil {
		return nil, fmt.Errorf("generate access token: %w", err)
	}

	refreshToken, _, err := m.generateToken(userID, phone, deviceID, m.refreshTokenTTL, TokenTypeRefresh)
	if err != nil {
		return nil, fmt.Errorf("generate refresh token: %w", err)
	}

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    expiresAt,
	}, nil
}

// GenerateAccessToken generates only access token
func (m *Manager) GenerateAccessToken(userID int64, phone, deviceID string) (string, time.Time, error) {
	return m.generateToken(userID, phone, deviceID, m.accessTokenTTL, TokenTypeAccess)
}

func (m *Manager) generateToken(userID int64, phone, deviceID string, ttl time.Duration, tokenType TokenType) (string, time.Time, error) {
	expiresAt := time.Now().Add(ttl)

	claims := Claims{
		UserID:    userID,
		Phone:     phone,
		DeviceID:  deviceID,
		TokenType: tokenType,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    m.issuer,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(m.secretKey)
	if err != nil {
		return "", time.Time{}, err
	}

	return tokenString, expiresAt, nil
}

// Parse parses and validates a token
func (m *Manager) Parse(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return m.secretKey, nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, fmt.Errorf("parse token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}

	return claims, nil
}

// ValidateAccessToken validates access token and checks token_type claim
func (m *Manager) ValidateAccessToken(tokenString string) (*Claims, error) {
	claims, err := m.Parse(tokenString)
	if err != nil {
		return nil, err
	}
	// Backward compatibility: accept tokens without token_type (pre-migration)
	if claims.TokenType != "" && claims.TokenType != TokenTypeAccess {
		return nil, ErrWrongTokenType
	}
	return claims, nil
}

// ValidateRefreshToken validates refresh token and checks token_type claim
func (m *Manager) ValidateRefreshToken(tokenString string) (*Claims, error) {
	claims, err := m.Parse(tokenString)
	if err != nil {
		return nil, err
	}
	// Backward compatibility: accept tokens without token_type (pre-migration)
	if claims.TokenType != "" && claims.TokenType != TokenTypeRefresh {
		return nil, ErrWrongTokenType
	}
	return claims, nil
}

// Refresh generates new token pair using refresh token
func (m *Manager) Refresh(refreshToken string) (*TokenPair, error) {
	claims, err := m.ValidateRefreshToken(refreshToken)
	if err != nil {
		return nil, err
	}

	return m.GenerateTokenPair(claims.UserID, claims.Phone, claims.DeviceID)
}

// Errors
var (
	ErrTokenExpired  = errors.New("token expired")
	ErrInvalidToken  = errors.New("invalid token")
	ErrWrongTokenType = errors.New("wrong token type")
)

