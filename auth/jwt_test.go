package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// NIAGA-344. generateToken took a tokenType argument and never serialized it,
// so an access token, a refresh token and a pending-2FA token were the same
// shape signed with the same key. ValidateToken checked signature and time and
// nothing else, which made all three interchangeable at every middleware in
// every service.
//
// These tests mint real tokens with a fixture key and put them through the real
// validator. Nothing here reads a configured secret or touches a real account.

const testSecret = "test-only-signing-key-not-a-credential" // secret-scan: allow

func newTestManager() *JWTManager {
	return NewJWTManager(testSecret, 15*time.Minute, 7*24*time.Hour)
}

func TestAccessTokenValidatesAndCarriesItsPurpose(t *testing.T) {
	m := newTestManager()
	userID := uuid.New()

	pair, err := m.GenerateTokenPair(userID, "a@example.test", "admin")
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}

	claims, err := m.ValidateToken(pair.AccessToken)
	if err != nil {
		t.Fatalf("access token must validate: %v", err)
	}
	if claims.TokenType != TokenTypeAccess {
		t.Errorf("TokenType = %q, want %q", claims.TokenType, TokenTypeAccess)
	}
	if claims.UserID != userID {
		t.Errorf("UserID = %v, want %v", claims.UserID, userID)
	}
	if claims.Role != "admin" {
		t.Errorf("Role = %q, want admin", claims.Role)
	}
	if claims.Purpose != "" {
		t.Errorf("Purpose = %q, want empty on an access token", claims.Purpose)
	}
}

// The bug, stated as a test: before this change both of these returned claims.
func TestRefreshTokenIsRejectedAsAnAccessToken(t *testing.T) {
	m := newTestManager()

	pair, err := m.GenerateTokenPair(uuid.New(), "a@example.test", "customer")
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}

	if _, err := m.ValidateToken(pair.RefreshToken); !errors.Is(err, ErrWrongTokenType) {
		t.Fatalf("ValidateToken(refresh) error = %v, want ErrWrongTokenType", err)
	}

	// It is still a perfectly good refresh token when asked for as one.
	claims, err := m.ValidateTokenOfType(pair.RefreshToken, TokenTypeRefresh)
	if err != nil {
		t.Fatalf("ValidateTokenOfType(refresh): %v", err)
	}
	if claims.TokenType != TokenTypeRefresh {
		t.Errorf("TokenType = %q, want %q", claims.TokenType, TokenTypeRefresh)
	}
}

func TestPendingTwoFactorTokenIsRejectedAsAnAccessToken(t *testing.T) {
	m := newTestManager()
	userID := uuid.New()

	temp, err := m.GenerateTempToken(userID, "a@example.test", "2fa_pending")
	if err != nil {
		t.Fatalf("GenerateTempToken: %v", err)
	}

	if _, err := m.ValidateToken(temp); !errors.Is(err, ErrWrongTokenType) {
		t.Fatalf("ValidateToken(temp) error = %v, want ErrWrongTokenType", err)
	}

	claims, err := m.ValidateTokenOfType(temp, TokenTypeTemp)
	if err != nil {
		t.Fatalf("ValidateTokenOfType(temp): %v", err)
	}
	if claims.Purpose != "2fa_pending" {
		t.Errorf("Purpose = %q, want 2fa_pending", claims.Purpose)
	}
	// Role has always carried the purpose too; twofactor_handler.go reads it,
	// so this asserts the old marker still works rather than silently dropping.
	if claims.Role != "2fa_pending" {
		t.Errorf("Role = %q, want 2fa_pending", claims.Role)
	}
}

// An access token must not be accepted where a temp token is required either —
// the check is an equality, not a ranking.
func TestAccessTokenIsRejectedWhereATempTokenIsRequired(t *testing.T) {
	m := newTestManager()

	pair, err := m.GenerateTokenPair(uuid.New(), "a@example.test", "admin")
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}
	if _, err := m.ValidateTokenOfType(pair.AccessToken, TokenTypeTemp); !errors.Is(err, ErrWrongTokenType) {
		t.Fatalf("error = %v, want ErrWrongTokenType", err)
	}
}

// Legacy compatibility, stated explicitly (an acceptance criterion): a token
// minted before this change has no token_type claim, and is refused rather than
// grandfathered in. Accepting it would BE the vulnerability — a pre-change
// refresh token is indistinguishable from a pre-change access token.
func TestLegacyTokenWithNoPurposeIsRefused(t *testing.T) {
	now := time.Now()
	legacy := &Claims{
		UserID: uuid.New(),
		Email:  "a@example.test",
		Role:   "admin",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ID:        uuid.New().String(),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, legacy).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign legacy token: %v", err)
	}

	if _, err := newTestManager().ValidateToken(signed); !errors.Is(err, ErrWrongTokenType) {
		t.Fatalf("legacy token error = %v, want ErrWrongTokenType", err)
	}
}

// Signature and expiry are still checked, and are checked BEFORE the purpose,
// so a wrong-purpose token and an expired one are not distinguishable by which
// check fired first.
func TestSignatureAndExpiryStillChecked(t *testing.T) {
	m := newTestManager()

	pair, err := m.GenerateTokenPair(uuid.New(), "a@example.test", "admin")
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}

	other := NewJWTManager("a-different-key", time.Minute, time.Hour) // secret-scan: allow
	if _, err := other.ValidateToken(pair.AccessToken); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("foreign key error = %v, want ErrInvalidToken", err)
	}

	expired := NewJWTManager(testSecret, -time.Minute, -time.Minute)
	stale, err := expired.GenerateTokenPair(uuid.New(), "a@example.test", "admin")
	if err != nil {
		t.Fatalf("GenerateTokenPair (expired): %v", err)
	}
	// Expired takes precedence over the purpose check even for a REFRESH token,
	// which would also fail the purpose test.
	if _, err := m.ValidateToken(stale.RefreshToken); !errors.Is(err, ErrExpiredToken) {
		t.Errorf("expired refresh token error = %v, want ErrExpiredToken", err)
	}
}

// ExtractClaims is unverified by design and stays that way; it must not become
// a way around the purpose check.
func TestExtractClaimsStillDoesNotVerify(t *testing.T) {
	m := newTestManager()
	pair, err := m.GenerateTokenPair(uuid.New(), "a@example.test", "admin")
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}
	claims, err := m.ExtractClaims(pair.RefreshToken)
	if err != nil {
		t.Fatalf("ExtractClaims: %v", err)
	}
	if claims.TokenType != TokenTypeRefresh {
		t.Errorf("TokenType = %q, want %q", claims.TokenType, TokenTypeRefresh)
	}
}
