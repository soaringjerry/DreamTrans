package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testAccessSecret  = "access-secret-that-is-longer-than-thirty-two-bytes"
	testRefreshSecret = "refresh-secret-that-is-different-and-long-enough"
)

func TestJWTManagerRequiresSeparateStrongSecrets(t *testing.T) {
	if _, err := NewJWTManagerWithSecrets("short", testRefreshSecret); err == nil {
		t.Fatal("expected short access secret to be rejected")
	}
	if _, err := NewJWTManagerWithSecrets(testAccessSecret, "short"); err == nil {
		t.Fatal("expected short refresh secret to be rejected")
	}
	if _, err := NewJWTManagerWithSecrets(testAccessSecret, testAccessSecret); err == nil {
		t.Fatal("expected reused signing secret to be rejected")
	}
}

func TestAccessAndRefreshTokensAreNotInterchangeable(t *testing.T) {
	manager, err := NewJWTManagerWithSecrets(testAccessSecret, testRefreshSecret)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	accessToken, err := manager.GenerateAccessToken("user-1", "tenant-1", "a@example.com", "user")
	if err != nil {
		t.Fatalf("generate access token: %v", err)
	}
	refreshToken, _, _, err := manager.GenerateRefreshToken("user-1")
	if err != nil {
		t.Fatalf("generate refresh token: %v", err)
	}

	if _, err := manager.ValidateAccessToken(accessToken); err != nil {
		t.Fatalf("access token should validate: %v", err)
	}
	if _, err := manager.ValidateRefreshToken(refreshToken); err != nil {
		t.Fatalf("refresh token should validate: %v", err)
	}
	if _, err := manager.ValidateAccessToken(refreshToken); err == nil {
		t.Fatal("refresh token must not validate as access token")
	}
	if _, err := manager.ValidateRefreshToken(accessToken); err == nil {
		t.Fatal("access token must not validate as refresh token")
	}
}

func TestAccessTokenRequiresIdentityAndExpiryClaims(t *testing.T) {
	manager, err := NewJWTManagerWithSecrets(testAccessSecret, testRefreshSecret)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	missingExpiry := jwt.NewWithClaims(jwt.SigningMethodHS256, UserClaims{
		UserID:    "user-1",
		TenantID:  "tenant-1",
		Email:     "a@example.com",
		Role:      "user",
		TokenType: "access",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:  "dreamtrans",
			Subject: "user-1",
			ID:      "token-1",
		},
	})
	tokenString, err := missingExpiry.SignedString([]byte(testAccessSecret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	if _, err := manager.ValidateAccessToken(tokenString); err == nil {
		t.Fatal("token without an expiry must be rejected")
	}

	mismatchedSubject := jwt.NewWithClaims(jwt.SigningMethodHS256, UserClaims{
		UserID:    "user-1",
		TenantID:  "tenant-1",
		Email:     "a@example.com",
		Role:      "user",
		TokenType: "access",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			Issuer:    "dreamtrans",
			Subject:   "user-2",
			ID:        "token-2",
		},
	})
	tokenString, err = mismatchedSubject.SignedString([]byte(testAccessSecret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	if _, err := manager.ValidateAccessToken(tokenString); err == nil {
		t.Fatal("token whose subject does not match user_id must be rejected")
	}
}
