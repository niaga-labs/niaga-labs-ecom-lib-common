package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

var (
	// ErrInvalidAPIKey is returned when API key is invalid.
	ErrInvalidAPIKey = errors.New("invalid API key")

	// ErrExpiredAPIKey is returned when API key has expired.
	ErrExpiredAPIKey = errors.New("expired API key")

	// ErrAPIKeyNotFound is returned when API key is not found.
	ErrAPIKeyNotFound = errors.New("API key not found")

	// ErrInsufficientScope is returned when API key lacks required scope.
	ErrInsufficientScope = errors.New("insufficient API key scope")
)

// APIKey represents an API key for service authentication.
type APIKey struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	KeyHash     string    `json:"-"`          // Stored hash, never expose
	KeyPrefix   string    `json:"key_prefix"` // First 8 chars for identification
	ServiceName string    `json:"service_name"`
	Scopes      []string  `json:"scopes"`
	RateLimit   int       `json:"rate_limit"` // Requests per minute
	ExpiresAt   time.Time `json:"expires_at,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	LastUsedAt  time.Time `json:"last_used_at,omitempty"`
	IsActive    bool      `json:"is_active"`
}

// APIKeyStore defines the interface for API key storage.
type APIKeyStore interface {
	// Create stores a new API key.
	Create(ctx context.Context, key *APIKey) error

	// GetByID retrieves an API key by ID.
	GetByID(ctx context.Context, id string) (*APIKey, error)

	// GetByHash retrieves an API key by its hash.
	GetByHash(ctx context.Context, hash string) (*APIKey, error)

	// Update updates an API key.
	Update(ctx context.Context, key *APIKey) error

	// Delete removes an API key.
	Delete(ctx context.Context, id string) error

	// List retrieves all API keys for a service.
	ListByService(ctx context.Context, serviceName string) ([]*APIKey, error)

	// UpdateLastUsed updates the last used timestamp.
	UpdateLastUsed(ctx context.Context, id string) error
}

// GenerateAPIKey generates a new API key.
// Returns the raw key (to show user once) and the APIKey struct with hash.
func GenerateAPIKey(name, serviceName string, scopes []string, expiresIn time.Duration) (string, *APIKey, error) {
	// Generate 32 bytes of random data
	rawBytes := make([]byte, 32)
	if _, err := rand.Read(rawBytes); err != nil {
		return "", nil, err
	}

	// Create key in format: klng_<service>_<random>
	prefix := "klng"
	svcPrefix := strings.ToLower(serviceName)[:3]
	randomPart := hex.EncodeToString(rawBytes)
	rawKey := prefix + "_" + svcPrefix + "_" + randomPart

	// Hash the key for storage
	hash := hashAPIKey(rawKey)

	var expiresAt time.Time
	if expiresIn > 0 {
		expiresAt = time.Now().Add(expiresIn)
	}

	apiKey := &APIKey{
		ID:          generateKeyID(),
		Name:        name,
		KeyHash:     hash,
		KeyPrefix:   rawKey[:12], // Show first 12 chars (klng_xxx_xxxx)
		ServiceName: serviceName,
		Scopes:      scopes,
		RateLimit:   1000, // Default 1000 req/min
		ExpiresAt:   expiresAt,
		CreatedAt:   time.Now(),
		IsActive:    true,
	}

	return rawKey, apiKey, nil
}

// ValidateAPIKey validates a raw API key against a stored key.
func ValidateAPIKey(rawKey string, storedKey *APIKey) error {
	if storedKey == nil {
		return ErrAPIKeyNotFound
	}

	if !storedKey.IsActive {
		return ErrInvalidAPIKey
	}

	// Check expiration
	if !storedKey.ExpiresAt.IsZero() && time.Now().After(storedKey.ExpiresAt) {
		return ErrExpiredAPIKey
	}

	// Constant-time comparison of hashes
	providedHash := hashAPIKey(rawKey)
	if subtle.ConstantTimeCompare([]byte(providedHash), []byte(storedKey.KeyHash)) != 1 {
		return ErrInvalidAPIKey
	}

	return nil
}

// HasScope checks if the API key has the required scope.
func (k *APIKey) HasScope(requiredScope string) bool {
	for _, scope := range k.Scopes {
		// Check exact match or wildcard
		if scope == requiredScope || scope == "*" {
			return true
		}

		// Check prefix wildcard (e.g., "inventory:*" matches "inventory:read")
		if strings.HasSuffix(scope, ":*") {
			prefix := strings.TrimSuffix(scope, "*")
			if strings.HasPrefix(requiredScope, prefix) {
				return true
			}
		}
	}
	return false
}

// HasAnyScope checks if the API key has any of the required scopes.
func (k *APIKey) HasAnyScope(requiredScopes []string) bool {
	for _, scope := range requiredScopes {
		if k.HasScope(scope) {
			return true
		}
	}
	return false
}

// hashAPIKey creates a SHA-256 hash of an API key.
func hashAPIKey(key string) string {
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}

// generateKeyID generates a unique key ID.
func generateKeyID() string {
	bytes := make([]byte, 8)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// Common scopes for service-to-service communication
const (
	ScopeAll            = "*"
	ScopeInventoryRead  = "inventory:read"
	ScopeInventoryWrite = "inventory:write"
	ScopeOrderRead      = "order:read"
	ScopeOrderWrite     = "order:write"
	ScopeAgentRead      = "agent:read"
	ScopeAgentWrite     = "agent:write"
	ScopeAdminAll       = "admin:*"
)

// PredefinedScopes returns common scope groups.
func PredefinedScopes() map[string][]string {
	return map[string][]string{
		"inventory-service": {ScopeInventoryRead, ScopeInventoryWrite, ScopeOrderRead},
		"order-service":     {ScopeOrderRead, ScopeOrderWrite, ScopeInventoryRead},
		"agent-service":     {ScopeAgentRead, ScopeAgentWrite, ScopeOrderRead},
		"admin-service":     {ScopeAdminAll},
		"readonly":          {ScopeInventoryRead, ScopeOrderRead, ScopeAgentRead},
	}
}
