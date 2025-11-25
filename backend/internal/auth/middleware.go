package auth

import (
	"context"
	"net/http"
	"strings"
)

type contextKey string

const (
	UserClaimsKey contextKey = "user_claims"
)

// AuthMiddleware creates HTTP middleware for JWT authentication
type AuthMiddleware struct {
	jwtManager *JWTManager
}

// NewAuthMiddleware creates a new auth middleware
func NewAuthMiddleware(jwtManager *JWTManager) *AuthMiddleware {
	return &AuthMiddleware{jwtManager: jwtManager}
}

// RequireAuth middleware requires valid JWT authentication
func (m *AuthMiddleware) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Get token from Authorization header
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, `{"error":"missing authorization header"}`, http.StatusUnauthorized)
			return
		}

		// Check Bearer prefix
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			http.Error(w, `{"error":"invalid authorization header format"}`, http.StatusUnauthorized)
			return
		}

		// Validate token
		claims, err := m.jwtManager.ValidateAccessToken(parts[1])
		if err != nil {
			if err == ErrExpiredToken {
				http.Error(w, `{"error":"token expired"}`, http.StatusUnauthorized)
				return
			}
			http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
			return
		}

		// Add claims to context
		ctx := context.WithValue(r.Context(), UserClaimsKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireRole middleware requires a specific role
func (m *AuthMiddleware) RequireRole(roles ...string) func(http.Handler) http.Handler {
	roleSet := make(map[string]bool)
	for _, r := range roles {
		roleSet[r] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := GetUserClaims(r.Context())
			if claims == nil {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}

			if !roleSet[claims.Role] {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// OptionalAuth middleware extracts JWT claims if present but doesn't require them
func (m *AuthMiddleware) OptionalAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader != "" {
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) == 2 && strings.ToLower(parts[0]) == "bearer" {
				if claims, err := m.jwtManager.ValidateAccessToken(parts[1]); err == nil {
					ctx := context.WithValue(r.Context(), UserClaimsKey, claims)
					r = r.WithContext(ctx)
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// GetUserClaims retrieves user claims from context
func GetUserClaims(ctx context.Context) *UserClaims {
	claims, ok := ctx.Value(UserClaimsKey).(*UserClaims)
	if !ok {
		return nil
	}
	return claims
}

// GetUserID retrieves user ID from context
func GetUserID(ctx context.Context) string {
	claims := GetUserClaims(ctx)
	if claims == nil {
		return ""
	}
	return claims.UserID
}

// GetTenantID retrieves tenant ID from context
func GetTenantID(ctx context.Context) string {
	claims := GetUserClaims(ctx)
	if claims == nil {
		return ""
	}
	return claims.TenantID
}

// GetUserRole retrieves user role from context
func GetUserRole(ctx context.Context) string {
	claims := GetUserClaims(ctx)
	if claims == nil {
		return ""
	}
	return claims.Role
}

// IsAdmin checks if the user is an admin or super_admin
func IsAdmin(ctx context.Context) bool {
	role := GetUserRole(ctx)
	return role == "admin" || role == "super_admin"
}

// IsSuperAdmin checks if the user is a super_admin
func IsSuperAdmin(ctx context.Context) bool {
	return GetUserRole(ctx) == "super_admin"
}
