package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

var (
	ErrInvalidToken = errors.New("invalid token")
	ErrExpiredToken = errors.New("token expired")
	// ErrWrongTokenType is returned when a token is valid and unexpired but was
	// minted for a different purpose than the caller requires — a refresh token
	// presented as an access token, or a pending-2FA token presented to a
	// protected route (NIAGA-344).
	ErrWrongTokenType = errors.New("wrong token type")
)

// Token purposes. Every token this manager mints carries exactly one, in the
// token_type claim, and ValidateTokenOfType refuses to return claims for any
// other. Before NIAGA-344 the purpose was passed to generateToken and then
// dropped on the floor, so all three kinds of token were interchangeable.
const (
	TokenTypeAccess  = "access"
	TokenTypeRefresh = "refresh"
	TokenTypeTemp    = "temp"
)

// Claims represents JWT custom claims
type Claims struct {
	UserID uuid.UUID `json:"user_id"`
	Email  string    `json:"email"`
	Role   string    `json:"role"`
	// TokenType is the purpose this token was minted for: one of
	// TokenTypeAccess, TokenTypeRefresh or TokenTypeTemp. It is omitempty so a
	// token issued before NIAGA-344 parses cleanly — and then fails the purpose
	// check, because an empty purpose matches nothing. That is deliberate: a
	// legacy token is exactly the refresh-token-as-access-token case this fixes,
	// so it must not be grandfathered in.
	TokenType string `json:"token_type,omitempty"`
	// Purpose narrows a temporary token further ("2fa_pending"). Empty on
	// access and refresh tokens.
	Purpose string `json:"purpose,omitempty"`
	jwt.RegisteredClaims
}

// TokenPair represents access and refresh tokens
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// JWTManager handles JWT operations
type JWTManager struct {
	secretKey       string
	accessTokenTTL  time.Duration
	refreshTokenTTL time.Duration
}

// NewJWTManager creates a new JWT manager
func NewJWTManager(secretKey string, accessTokenTTL, refreshTokenTTL time.Duration) *JWTManager {
	return &JWTManager{
		secretKey:       secretKey,
		accessTokenTTL:  accessTokenTTL,
		refreshTokenTTL: refreshTokenTTL,
	}
}

// GenerateTokenPair generates access and refresh tokens
func (m *JWTManager) GenerateTokenPair(userID uuid.UUID, email, role string) (*TokenPair, error) {
	accessToken, err := m.generateToken(userID, email, role, "", m.accessTokenTTL, TokenTypeAccess)
	if err != nil {
		return nil, err
	}

	refreshToken, err := m.generateToken(userID, email, role, "", m.refreshTokenTTL, TokenTypeRefresh)
	if err != nil {
		return nil, err
	}

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    int64(m.accessTokenTTL.Seconds()),
	}, nil
}

// generateToken generates a JWT token carrying its purpose.
//
// The tokenType argument used to be accepted and discarded, which is what made
// every kind of token interchangeable (NIAGA-344). It is now a claim.
func (m *JWTManager) generateToken(userID uuid.UUID, email, role, purpose string, ttl time.Duration, tokenType string) (string, error) {
	now := time.Now()
	claims := &Claims{
		UserID:    userID,
		Email:     email,
		Role:      role,
		TokenType: tokenType,
		Purpose:   purpose,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ID:        uuid.New().String(),
			Subject:   userID.String(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(m.secretKey))
}

// ValidateToken validates an ACCESS token and returns its claims.
//
// The name and signature are unchanged on purpose: every middleware in every
// service already calls this, and the safe behaviour should be the one they get
// without editing them. A refresh token or a pending-2FA token now fails here
// with ErrWrongTokenType (NIAGA-344). Callers that genuinely want another kind
// of token must say so, with ValidateTokenOfType.
func (m *JWTManager) ValidateToken(tokenString string) (*Claims, error) {
	return m.ValidateTokenOfType(tokenString, TokenTypeAccess)
}

// ValidateTokenOfType validates a token and requires it to carry the given
// purpose. Signature, method and expiry are checked first, so a caller cannot
// tell a wrong-purpose token from an expired one by timing alone.
func (m *JWTManager) ValidateTokenOfType(tokenString, want string) (*Claims, error) {
	claims, err := m.parseToken(tokenString)
	if err != nil {
		return nil, err
	}
	if claims.TokenType != want {
		return nil, ErrWrongTokenType
	}
	return claims, nil
}

// parseToken checks signature, signing method and time, and nothing else.
// Unexported: a purpose check is never optional at a call site.
func (m *JWTManager) parseToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return []byte(m.secretKey), nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpiredToken
		}
		return nil, ErrInvalidToken
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}

	return claims, nil
}

// ExtractClaims extracts claims from a token without validation (for refresh)
func (m *JWTManager) ExtractClaims(tokenString string) (*Claims, error) {
	token, _, err := jwt.NewParser().ParseUnverified(tokenString, &Claims{})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok {
		return nil, ErrInvalidToken
	}

	return claims, nil
}

// GenerateTempToken generates a short-lived temporary token for purposes like
// 2FA verification.
//
// The purpose is now carried in its own claim AND left in Role, where this
// function has always put it: twofactor_handler.go gates on
// claims.Role == "2fa_pending", and moving that out from under it is not this
// ticket's job. The gate that matters is the token_type claim — a temp token no
// longer passes ValidateToken at all, whatever its role says.
func (m *JWTManager) GenerateTempToken(userID uuid.UUID, email, purpose string) (string, error) {
	// Temporary tokens are valid for 5 minutes
	tempTTL := 5 * time.Minute
	return m.generateToken(userID, email, purpose, purpose, tempTTL, TokenTypeTemp)
}

// ExtractRoleFromToken is DEPRECATED and removed for security reasons.
// SECURITY: Never extract claims without signature verification.
// Always use JWTManager.ValidateToken() to verify tokens before trusting claims.
// This function previously allowed privilege escalation attacks by accepting
// forged tokens with arbitrary role claims.
