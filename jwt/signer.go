package jwt

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Signer mints RS256 tokens with the issuer's private key. It is constructed
// ONLY by the token issuer (cg-users auth). Every other service verifies with a
// Validator and never holds signing material.
//
// The public mint surface mirrors Manager exactly (GenerateAccessToken /
// GenerateTokenPair / GenerateTokenPairWithContext / Refresh), so issuer call
// sites switch from *Manager to *Signer with no signature changes — only the
// algorithm (RS256) and key (private) differ, plus a kid in the header.
type Signer struct {
	priv       *rsa.PrivateKey
	kid        string
	issuer     string
	accessTTL  time.Duration
	refreshTTL time.Duration
}

// NewSigner parses the RSA private key (inline PEM or a mounted file) and
// returns a Signer.
func NewSigner(cfg Config) (*Signer, error) {
	pemData := cfg.PrivateKeyPEM
	if pemData == "" && cfg.PrivateKeyPath != "" {
		b, err := os.ReadFile(cfg.PrivateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("jwt: read private key file %q: %w", cfg.PrivateKeyPath, err)
		}
		pemData = string(b)
	}
	if pemData == "" {
		return nil, errors.New("jwt: private key is required for signer (set PrivateKeyPEM or PrivateKeyPath)")
	}
	if cfg.SigningKeyID == "" {
		return nil, errors.New("jwt: signing kid is required for signer")
	}
	priv, err := parseRSAPrivateKeyPEM(pemData)
	if err != nil {
		return nil, fmt.Errorf("jwt: parse private key: %w", err)
	}

	accessTTL := cfg.AccessTokenTTL
	if accessTTL == 0 {
		accessTTL = 15 * time.Minute
	}
	refreshTTL := cfg.RefreshTokenTTL
	if refreshTTL == 0 {
		refreshTTL = 720 * time.Hour
	}

	return &Signer{
		priv:       priv,
		kid:        cfg.SigningKeyID,
		issuer:     cfg.Issuer,
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
	}, nil
}

// GenerateTokenPair mints an access + refresh pair without app context.
func (s *Signer) GenerateTokenPair(userID int64, phone, deviceID string) (*TokenPair, error) {
	return s.GenerateTokenPairWithContext(userID, phone, deviceID, AppContext{})
}

// GenerateTokenPairWithContext mints an access + refresh pair carrying app context.
func (s *Signer) GenerateTokenPairWithContext(userID int64, phone, deviceID string, appCtx AppContext) (*TokenPair, error) {
	accessToken, expiresAt, err := s.generateToken(userID, phone, deviceID, appCtx, s.accessTTL, TokenTypeAccess)
	if err != nil {
		return nil, fmt.Errorf("generate access token: %w", err)
	}
	refreshToken, refreshExpiresAt, err := s.generateToken(userID, phone, deviceID, appCtx, s.refreshTTL, TokenTypeRefresh)
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

// GenerateAccessToken mints a single access token (service-to-service convention).
func (s *Signer) GenerateAccessToken(userID int64, phone, deviceID string) (string, time.Time, error) {
	return s.generateToken(userID, phone, deviceID, AppContext{}, s.accessTTL, TokenTypeAccess)
}

func (s *Signer) generateToken(userID int64, phone, deviceID string, appCtx AppContext, ttl time.Duration, tokenType TokenType) (string, time.Time, error) {
	claims, expiresAt := buildClaims(userID, phone, deviceID, appCtx, ttl, tokenType, s.issuer)

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = s.kid

	tokenString, err := token.SignedString(s.priv)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign token: %w", err)
	}
	return tokenString, expiresAt, nil
}

// Refresh validates a refresh token against the Signer's OWN public key (no
// network/JWKS dependency — the issuer must be able to refresh even if its own
// JWKS endpoint is briefly unreachable) and mints a new pair, preserving app
// context claims.
func (s *Signer) Refresh(refreshToken string) (*TokenPair, error) {
	claims, err := s.parseSelf(refreshToken)
	if err != nil {
		return nil, err
	}
	if claims.TokenType != TokenTypeRefresh {
		return nil, ErrWrongTokenType
	}
	appCtx := AppContext{
		App:     claims.App,
		OrgID:   claims.OrgID,
		OrgType: claims.OrgType,
		CityID:  claims.CityID,
		OrgRole: claims.OrgRole,
	}
	return s.GenerateTokenPairWithContext(claims.UserID, claims.Phone, claims.DeviceID, appCtx)
}

func (s *Signer) parseSelf(tokenString string) (*Claims, error) {
	opts := []jwt.ParserOption{jwt.WithValidMethods([]string{"RS256"})}
	if s.issuer != "" {
		opts = append(opts, jwt.WithIssuer(s.issuer))
	}
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("%w: unexpected signing method", ErrInvalidToken)
		}
		return &s.priv.PublicKey, nil
	}, opts...)
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

// KeyID returns the signing key id (kid) stamped into minted tokens.
func (s *Signer) KeyID() string { return s.kid }

// PublicKey returns a defensive copy of the issuer's RSA public key, so callers
// cannot mutate the Signer's internal key material through the returned pointer.
func (s *Signer) PublicKey() *rsa.PublicKey {
	pub := s.priv.PublicKey
	pub.N = new(big.Int).Set(s.priv.PublicKey.N)
	return &pub
}

// JWKSJSON returns the issuer's public key as a JWKS document, ready to serve
// from cg-users' /.well-known/jwks.json endpoint.
func (s *Signer) JWKSJSON() ([]byte, error) {
	return json.Marshal(jwkSet{Keys: []jwkKey{rsaPublicToJWK(&s.priv.PublicKey, s.kid)}})
}

func parseRSAPrivateKeyPEM(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("no PEM block found")
	}
	// Accept both PKCS#1 ("RSA PRIVATE KEY") and PKCS#8 ("PRIVATE KEY").
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rsaKey, ok := keyAny.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("PEM is not an RSA private key (got %T)", keyAny)
	}
	return rsaKey, nil
}
