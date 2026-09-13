package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/niaga-labs/niaga-labs-ecom-lib-common/response"
)

// CSRFProtection implements double-submit cookie pattern for CSRF protection
// This middleware validates that the X-CSRF-Token header matches the csrf_token cookie
// Only applies to state-changing methods (POST, PUT, PATCH, DELETE)
func CSRFProtection() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip for safe methods (GET, HEAD, OPTIONS)
		method := c.Request.Method
		if method == "GET" || method == "HEAD" || method == "OPTIONS" {
			c.Next()
			return
		}

		// Get CSRF token from cookie
		cookieToken, err := c.Cookie("csrf_token")
		if err != nil || cookieToken == "" {
			response.Forbidden(c, "CSRF token missing from cookie")
			c.Abort()
			return
		}

		// Get CSRF token from header
		headerToken := c.GetHeader("X-CSRF-Token")
		if headerToken == "" {
			response.Forbidden(c, "CSRF token missing from header")
			c.Abort()
			return
		}

		// Compare tokens (double-submit pattern)
		if cookieToken != headerToken {
			response.Forbidden(c, "CSRF token mismatch")
			c.Abort()
			return
		}

		c.Next()
	}
}

// CSRFProtectionWithSkip allows skipping CSRF for specific paths (e.g., webhook endpoints)
func CSRFProtectionWithSkip(skipPaths ...string) gin.HandlerFunc {
	skipMap := make(map[string]bool)
	for _, path := range skipPaths {
		skipMap[path] = true
	}

	return func(c *gin.Context) {
		// Skip if path is in skip list
		if skipMap[c.Request.URL.Path] {
			c.Next()
			return
		}

		// Skip for safe methods (GET, HEAD, OPTIONS)
		method := c.Request.Method
		if method == "GET" || method == "HEAD" || method == "OPTIONS" {
			c.Next()
			return
		}

		// Get CSRF token from cookie
		cookieToken, err := c.Cookie("csrf_token")
		if err != nil || cookieToken == "" {
			response.Forbidden(c, "CSRF token missing from cookie")
			c.Abort()
			return
		}

		// Get CSRF token from header
		headerToken := c.GetHeader("X-CSRF-Token")
		if headerToken == "" {
			response.Forbidden(c, "CSRF token missing from header")
			c.Abort()
			return
		}

		// Compare tokens (double-submit pattern)
		if cookieToken != headerToken {
			response.Forbidden(c, "CSRF token mismatch")
			c.Abort()
			return
		}

		c.Next()
	}
}

// OptionalCSRFProtection only validates CSRF if the cookie is present
// This allows backward compatibility with clients that don't support CSRF yet
func OptionalCSRFProtection() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip for safe methods (GET, HEAD, OPTIONS)
		method := c.Request.Method
		if method == "GET" || method == "HEAD" || method == "OPTIONS" {
			c.Next()
			return
		}

		// Check if CSRF cookie is present
		cookieToken, err := c.Cookie("csrf_token")
		if err != nil || cookieToken == "" {
			// No CSRF cookie - skip validation (backward compatibility)
			c.Next()
			return
		}

		// CSRF cookie present - require matching header
		headerToken := c.GetHeader("X-CSRF-Token")
		if headerToken == "" {
			response.Forbidden(c, "CSRF token missing from header")
			c.Abort()
			return
		}

		// Compare tokens
		if cookieToken != headerToken {
			response.Forbidden(c, "CSRF token mismatch")
			c.Abort()
			return
		}

		c.Next()
	}
}
