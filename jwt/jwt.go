package jwt

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Config holds JWT configuration.
//
// Three concerns are layered here so a single struct can drive the whole
// HS256 -> RS256 migration without forcing a lockstep deploy:
//   - HS256 (legacy): SecretKey. Used by Manager (sign+verify) and, while
//     AcceptHS256 is true, by Validator for backward-compatible verification.
//   - RS256 signing (issuer only): PrivateKeyPEM + SigningKeyID. Used by Signer.
//   - RS256 verification (everyone): JWKSURL. Used by Validator.
//
// Cutover gates (AcceptHS256/SignWithRS256) let each service flip behavior
// independently via env without a code change.
type Config struct {
	SecretKey       string        `yaml:"secret_key" env:"JWT_SECRET_KEY"`
	AccessTokenTTL  time.Duration `yaml:"access_token_ttl" env:"JWT_ACCESS_TOKEN_TTL" env-default:"15m"`
	RefreshTokenTTL time.Duration `yaml:"refresh_token_ttl" env:"JWT_REFRESH_TOKEN_TTL" env-default:"720h"` // 30 days
	Issuer          string        `yaml:"issuer" env:"JWT_ISSUER" env-default:"cg-platform"`

	// RS256 signing material — set ONLY on the issuer (cg-users auth).
	// Provide the key either inline (PrivateKeyPEM, e.g. helm --set-file) or as a
	// mounted file (PrivateKeyPath, e.g. a docker-compose secret volume). PEM
	// takes precedence when both are set.
	PrivateKeyPEM  string `yaml:"private_key_pem" env:"JWT_PRIVATE_KEY_PEM"`   // PKCS#8 or PKCS#1 RSA private key, PEM-encoded
	PrivateKeyPath string `yaml:"private_key_path" env:"JWT_PRIVATE_KEY_PATH"` // path to a PEM key file (mounted secret); read if PrivateKeyPEM is empty
	SigningKeyID   string `yaml:"signing_kid" env:"JWT_SIGNING_KID"`           // kid stamped into the token header, e.g. "cg-users-2026-06"

	// RS256 verification — set on every verifying service.
	JWKSURL     string        `yaml:"jwks_url" env:"JWT_JWKS_URL"`
	JWKSRefresh time.Duration `yaml:"jwks_refresh" env:"JWT_JWKS_REFRESH" env-default:"15m"`
	JWKSTimeout time.Duration `yaml:"jwks_timeout" env:"JWT_JWKS_TIMEOUT" env-default:"5s"`

	// ExpectedIssuer, when non-empty, makes Validator reject tokens whose `iss`
	// claim differs. Leave EMPTY during the dual-accept window — services
	// currently mint with inconsistent issuers (cg-platform / amocrm-sync /
	// organization-service); enable only after the central issuer (cg-users)
	// makes `iss` uniform across all minted tokens.
	ExpectedIssuer string `yaml:"expected_issuer" env:"JWT_EXPECTED_ISSUER"`

	// Cutover gates.
	AcceptHS256   bool `yaml:"accept_hs256" env:"JWT_ACCEPT_HS256" env-default:"true"` // Validator also accepts legacy HS256; flip false at end of migration
	SignWithRS256 bool `yaml:"sign_rs256" env:"JWT_SIGN_RS256" env-default:"false"`    // issuer mints RS256 instead of HS256 (consumed by cg-users wiring)
}

// TokenType represents the type of JWT token
type TokenType string

const (
	TokenTypeAccess  TokenType = "access"
	TokenTypeRefresh TokenType = "refresh"
)

// AppContext identifies which app the token was issued for and (optionally)
// which organization the partner user has selected.
// Backward compat: tokens without the App field are treated as app="client".
type AppContext struct {
	// App identifies the application: "client" or "partner".
	// Empty string is treated as "client" for backward compatibility.
	App     string `json:"app,omitempty"`
	OrgID   string `json:"org_id,omitempty"`
	OrgType string `json:"org_type,omitempty"`
	CityID  int64  `json:"city_id,omitempty"`
	OrgRole string `json:"org_role,omitempty"`
	// OrgIDs is the user's complete active organization-membership set. It is
	// independent from OrgID, which represents an optional selected org.
	OrgIDs []string `json:"orgs"`
}

// Claims represents JWT claims
type Claims struct {
	UserID    int64     `json:"user_id"`
	Phone     string    `json:"phone,omitempty"`
	DeviceID  string    `json:"device_id,omitempty"`
	TokenType TokenType `json:"token_type,omitempty"`

	// App context claims (optional; backward-compat: absent = "client")
	App     string   `json:"app,omitempty"`
	OrgID   string   `json:"org_id,omitempty"`
	OrgType string   `json:"org_type,omitempty"`
	CityID  int64    `json:"city_id,omitempty"`
	OrgRole string   `json:"org_role,omitempty"`
	OrgIDs  []string `json:"orgs"`

	jwt.RegisteredClaims
}

// TokenPair contains access and refresh tokens
type TokenPair struct {
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	ExpiresAt        time.Time `json:"expires_at"`         // Access token expiry (sent to client)
	RefreshExpiresAt time.Time `json:"refresh_expires_at"` // Refresh token expiry (for session storage)
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
	if len(cfg.SecretKey) < 32 {
		return nil, fmt.Errorf("jwt: secret key must be at least 32 bytes, got %d", len(cfg.SecretKey))
	}

	return &Manager{
		secretKey:       []byte(cfg.SecretKey),
		accessTokenTTL:  cfg.AccessTokenTTL,
		refreshTokenTTL: cfg.RefreshTokenTTL,
		issuer:          cfg.Issuer,
	}, nil
}

// GenerateTokenPair generates access and refresh tokens without app context.
// Backward-compatible: tokens produced here carry no app claim (treated as "client").
func (m *Manager) GenerateTokenPair(userID int64, phone, deviceID string) (*TokenPair, error) {
	return m.GenerateTokenPairWithContext(userID, phone, deviceID, AppContext{})
}

// GenerateTokenPairWithContext generates access and refresh tokens with optional app context.
// Use this when issuing tokens for the partner app or when an org has been selected.
func (m *Manager) GenerateTokenPairWithContext(userID int64, phone, deviceID string, appCtx AppContext) (*TokenPair, error) {
	accessToken, expiresAt, err := m.generateToken(userID, phone, deviceID, appCtx, m.accessTokenTTL, TokenTypeAccess)
	if err != nil {
		return nil, fmt.Errorf("generate access token: %w", err)
	}

	refreshToken, refreshExpiresAt, err := m.generateToken(userID, phone, deviceID, appCtx, m.refreshTokenTTL, TokenTypeRefresh)
	if err != nil {
		return nil, fmt.Errorf("generate refresh token: %w", err)
	}

	return &TokenPair{
		AccessToken:      accessToken,
		RefreshToken:     refreshToken,
		ExpiresAt:        expiresAt,
		RefreshExpiresAt: refreshExpiresAt,
	}, nil
}

// GenerateAccessToken generates only access token without app context.
func (m *Manager) GenerateAccessToken(userID int64, phone, deviceID string) (string, time.Time, error) {
	return m.generateToken(userID, phone, deviceID, AppContext{}, m.accessTokenTTL, TokenTypeAccess)
}

func (m *Manager) generateToken(userID int64, phone, deviceID string, appCtx AppContext, ttl time.Duration, tokenType TokenType) (string, time.Time, error) {
	claims, expiresAt := buildClaims(userID, phone, deviceID, appCtx, ttl, tokenType, m.issuer)

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(m.secretKey)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign token: %w", err)
	}

	return tokenString, expiresAt, nil
}

// buildClaims assembles the canonical Claims for a token. Shared by the HS256
// Manager and the RS256 Signer so both produce byte-identical claim sets —
// only the signing method/key differs. Keeping this in one place guarantees
// service-to-service tokens look the same before and after the migration.
func buildClaims(userID int64, phone, deviceID string, appCtx AppContext, ttl time.Duration, tokenType TokenType, issuer string) (Claims, time.Time) {
	now := time.Now()
	expiresAt := now.Add(ttl)

	claims := Claims{
		UserID:    userID,
		Phone:     phone,
		DeviceID:  deviceID,
		TokenType: tokenType,
		App:       appCtx.App,
		OrgID:     appCtx.OrgID,
		OrgType:   appCtx.OrgType,
		CityID:    appCtx.CityID,
		OrgRole:   appCtx.OrgRole,
		OrgIDs:    slices.Clone(appCtx.OrgIDs),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
			Issuer:    issuer,
		},
	}

	return claims, expiresAt
}

// Parse parses and validates a token
func (m *Manager) Parse(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return m.secretKey, nil
	}, jwt.WithValidMethods([]string{"HS256"})) // defense-in-depth: pin alg before the keyfunc runs

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
	if claims.TokenType != TokenTypeAccess {
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
	if claims.TokenType != TokenTypeRefresh {
		return nil, ErrWrongTokenType
	}
	return claims, nil
}

// Refresh generates new token pair using refresh token, preserving app context claims.
func (m *Manager) Refresh(refreshToken string) (*TokenPair, error) {
	claims, err := m.ValidateRefreshToken(refreshToken)
	if err != nil {
		return nil, err
	}

	appCtx := AppContext{
		App:     claims.App,
		OrgID:   claims.OrgID,
		OrgType: claims.OrgType,
		CityID:  claims.CityID,
		OrgRole: claims.OrgRole,
		OrgIDs:  slices.Clone(claims.OrgIDs),
	}
	return m.GenerateTokenPairWithContext(claims.UserID, claims.Phone, claims.DeviceID, appCtx)
}

// Close releases resources. It is a no-op for Manager (HS256 holds none); it
// exists so Manager satisfies the same Verifier interface as Validator, letting
// services swap one for the other without touching shutdown wiring.
func (m *Manager) Close() error { return nil }
