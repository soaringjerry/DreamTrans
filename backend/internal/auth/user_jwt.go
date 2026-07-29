package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

var (
	ErrInvalidToken = errors.New("invalid token")
	ErrExpiredToken = errors.New("token has expired")
)

// UserClaims represents the JWT claims for user authentication
type UserClaims struct {
	UserID    string `json:"user_id"`
	TenantID  string `json:"tenant_id"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	TokenType string `json:"token_type"`
	jwt.RegisteredClaims
}

type refreshClaims struct {
	TokenType string `json:"token_type"`
	jwt.RegisteredClaims
}

// JWTManager handles JWT token operations
type JWTManager struct {
	accessSecret  []byte
	refreshSecret []byte
	accessExpiry  time.Duration
	refreshExpiry time.Duration
}

// NewJWTManager creates a new JWT manager
func NewJWTManager() (*JWTManager, error) {
	return NewJWTManagerWithSecrets(os.Getenv("JWT_SECRET"), os.Getenv("JWT_REFRESH_SECRET"))
}

// NewJWTManagerWithSecrets creates a JWT manager with separate signing keys.
// Keeping refresh tokens on a different key limits the impact of an access-key leak.
func NewJWTManagerWithSecrets(accessSecret, refreshSecret string) (*JWTManager, error) {
	if len(accessSecret) < 32 {
		return nil, fmt.Errorf("JWT_SECRET must be at least 32 characters")
	}
	if len(refreshSecret) < 32 {
		return nil, fmt.Errorf("JWT_REFRESH_SECRET must be at least 32 characters")
	}
	if accessSecret == refreshSecret {
		return nil, fmt.Errorf("JWT_SECRET and JWT_REFRESH_SECRET must be different")
	}

	return &JWTManager{
		accessSecret:  []byte(accessSecret),
		refreshSecret: []byte(refreshSecret),
		accessExpiry:  15 * time.Minute,   // Access token expires in 15 minutes
		refreshExpiry: 7 * 24 * time.Hour, // Refresh token expires in 7 days
	}, nil
}

// GenerateAccessToken generates a JWT access token
func (m *JWTManager) GenerateAccessToken(userID, tenantID, email, role string) (string, error) {
	claims := UserClaims{
		UserID:    userID,
		TenantID:  tenantID,
		Email:     email,
		Role:      role,
		TokenType: "access",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(m.accessExpiry)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
			Issuer:    "dreamtrans",
			Subject:   userID,
			ID:        uuid.NewString(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(m.accessSecret)
}

// GenerateRefreshToken generates a refresh token and returns token string and its hash
func (m *JWTManager) GenerateRefreshToken(userID string) (tokenString string, tokenHash string, expiresAt time.Time, err error) {
	expiresAt = time.Now().Add(m.refreshExpiry)

	claims := refreshClaims{
		TokenType: "refresh",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
			Issuer:    "dreamtrans",
			Subject:   userID,
			ID:        uuid.NewString(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err = token.SignedString(m.refreshSecret)
	if err != nil {
		return "", "", time.Time{}, err
	}

	// Generate hash for storage
	hash := sha256.Sum256([]byte(tokenString))
	tokenHash = hex.EncodeToString(hash[:])

	return tokenString, tokenHash, expiresAt, nil
}

// ValidateAccessToken validates an access token and returns the claims
func (m *JWTManager) ValidateAccessToken(tokenString string) (*UserClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &UserClaims{}, func(token *jwt.Token) (interface{}, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return m.accessSecret, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer("dreamtrans"),
		jwt.WithExpirationRequired(),
	)

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpiredToken
		}
		return nil, ErrInvalidToken
	}

	claims, ok := token.Claims.(*UserClaims)
	if !ok || !token.Valid || claims.TokenType != "access" ||
		claims.UserID == "" || claims.Subject != claims.UserID ||
		claims.TenantID == "" || claims.Email == "" ||
		(claims.Role != "user" && claims.Role != "admin" && claims.Role != "super_admin") ||
		claims.ID == "" {
		return nil, ErrInvalidToken
	}

	return claims, nil
}

// ValidateRefreshToken validates a refresh token and returns the user ID
func (m *JWTManager) ValidateRefreshToken(tokenString string) (string, error) {
	token, err := jwt.ParseWithClaims(tokenString, &refreshClaims{}, func(token *jwt.Token) (interface{}, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return m.refreshSecret, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer("dreamtrans"),
		jwt.WithExpirationRequired(),
	)

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return "", ErrExpiredToken
		}
		return "", ErrInvalidToken
	}

	claims, ok := token.Claims.(*refreshClaims)
	if !ok || !token.Valid || claims.TokenType != "refresh" || claims.Subject == "" || claims.ID == "" {
		return "", ErrInvalidToken
	}

	return claims.Subject, nil
}

// HashRefreshToken creates a hash of a refresh token for storage
func HashRefreshToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

// AccessTokenExpiry returns the access token expiry duration
func (m *JWTManager) AccessTokenExpiry() time.Duration {
	return m.accessExpiry
}

// RefreshTokenExpiry returns the refresh token expiry duration
func (m *JWTManager) RefreshTokenExpiry() time.Duration {
	return m.refreshExpiry
}
