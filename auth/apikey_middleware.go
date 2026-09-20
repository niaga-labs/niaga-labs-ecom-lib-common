package auth

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	// APIKeyHeader is the header name for API key authentication.
	APIKeyHeader = "X-API-Key"

	// APIKeyContextKey is the context key for the validated API key.
	APIKeyContextKey = "api_key"
)

// APIKeyMiddlewareConfig holds configuration for API key middleware.
type APIKeyMiddlewareConfig struct {
	Store             APIKeyStore
	Logger            *zap.Logger
	RequiredScopes    []string // Scopes required for this endpoint
	AllowQueryParam   bool     // Allow API key in query param ?api_key=xxx
	CacheTTL          time.Duration
	EnableRateLimiter bool
}

// APIKeyCache caches validated API keys to reduce database lookups.
type APIKeyCache struct {
	mu    sync.RWMutex
	cache map[string]*cacheEntry
	ttl   time.Duration
}

type cacheEntry struct {
	key       *APIKey
	expiresAt time.Time
}

// NewAPIKeyCache creates a new API key cache.
func NewAPIKeyCache(ttl time.Duration) *APIKeyCache {
	c := &APIKeyCache{
		cache: make(map[string]*cacheEntry),
		ttl:   ttl,
	}

	// Start cleanup goroutine
	go c.cleanup()

	return c
}

func (c *APIKeyCache) Get(hash string) (*APIKey, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.cache[hash]
	if !ok || time.Now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.key, true
}

func (c *APIKeyCache) Set(hash string, key *APIKey) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache[hash] = &cacheEntry{
		key:       key,
		expiresAt: time.Now().Add(c.ttl),
	}
}

func (c *APIKeyCache) Delete(hash string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cache, hash)
}

func (c *APIKeyCache) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for hash, entry := range c.cache {
			if now.After(entry.expiresAt) {
				delete(c.cache, hash)
			}
		}
		c.mu.Unlock()
	}
}

// APIKeyMiddleware creates Gin middleware for API key authentication.
func APIKeyMiddleware(config APIKeyMiddlewareConfig) gin.HandlerFunc {
	var cache *APIKeyCache
	if config.CacheTTL > 0 {
		cache = NewAPIKeyCache(config.CacheTTL)
	}

	return func(c *gin.Context) {
		// Extract API key from header or query param
		rawKey := c.GetHeader(APIKeyHeader)
		if rawKey == "" && config.AllowQueryParam {
			rawKey = c.Query("api_key")
		}

		if rawKey == "" {
			config.Logger.Debug("Missing API key")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "API key required",
			})
			return
		}

		// Hash the provided key
		keyHash := hashAPIKey(rawKey)

		// Check cache first
		var apiKey *APIKey
		var err error

		if cache != nil {
			apiKey, _ = cache.Get(keyHash)
		}

		// Cache miss - lookup in store
		if apiKey == nil {
			apiKey, err = config.Store.GetByHash(c.Request.Context(), keyHash)
			if err != nil {
				config.Logger.Debug("API key not found", zap.Error(err))
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
					"error": "Invalid API key",
				})
				return
			}

			// Cache the result
			if cache != nil {
				cache.Set(keyHash, apiKey)
			}
		}

		// Validate the key
		if err := ValidateAPIKey(rawKey, apiKey); err != nil {
			config.Logger.Debug("API key validation failed",
				zap.String("key_id", apiKey.ID),
				zap.Error(err))

			if err == ErrExpiredAPIKey {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
					"error": "API key has expired",
				})
				return
			}

			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "Invalid API key",
			})
			return
		}

		// Check required scopes
		if len(config.RequiredScopes) > 0 {
			if !apiKey.HasAnyScope(config.RequiredScopes) {
				config.Logger.Debug("Insufficient scope",
					zap.String("key_id", apiKey.ID),
					zap.Strings("required", config.RequiredScopes),
					zap.Strings("has", apiKey.Scopes))
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
					"error": "Insufficient API key scope",
				})
				return
			}
		}

		// Update last used timestamp (async)
		go func() {
			config.Store.UpdateLastUsed(c.Request.Context(), apiKey.ID)
		}()

		// Store API key in context
		c.Set(APIKeyContextKey, apiKey)

		config.Logger.Debug("API key authenticated",
			zap.String("key_id", apiKey.ID),
			zap.String("service", apiKey.ServiceName))

		c.Next()
	}
}

// GetAPIKeyFromContext retrieves the API key from Gin context.
func GetAPIKeyFromContext(c *gin.Context) *APIKey {
	if val, exists := c.Get(APIKeyContextKey); exists {
		if apiKey, ok := val.(*APIKey); ok {
			return apiKey
		}
	}
	return nil
}

// RequireScope creates middleware that requires specific scopes.
func RequireScope(requiredScopes ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey := GetAPIKeyFromContext(c)
		if apiKey == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "API key required",
			})
			return
		}

		if !apiKey.HasAnyScope(requiredScopes) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error":    "Insufficient scope",
				"required": requiredScopes,
			})
			return
		}

		c.Next()
	}
}

// ServiceAuthClient is an HTTP client that automatically adds API key authentication.
type ServiceAuthClient struct {
	client  *http.Client
	apiKey  string
	baseURL string
}

// NewServiceAuthClient creates a new authenticated HTTP client.
func NewServiceAuthClient(apiKey, baseURL string) *ServiceAuthClient {
	return &ServiceAuthClient{
		client:  &http.Client{Timeout: 30 * time.Second},
		apiKey:  apiKey,
		baseURL: strings.TrimSuffix(baseURL, "/"),
	}
}

// Do executes an HTTP request with API key authentication.
func (c *ServiceAuthClient) Do(req *http.Request) (*http.Response, error) {
	req.Header.Set(APIKeyHeader, c.apiKey)
	return c.client.Do(req)
}

// Get performs a GET request.
func (c *ServiceAuthClient) Get(path string) (*http.Response, error) {
	req, err := http.NewRequest("GET", c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}
