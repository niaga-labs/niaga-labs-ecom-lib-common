package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/niaga-labs/niaga-labs-ecom-lib-common/auth"
	"github.com/niaga-labs/niaga-labs-ecom-lib-common/response"
)

// AuthMiddleware validates JWT tokens
// Supports both cookie-based (web) and Authorization header (mobile/API) authentication
func AuthMiddleware(jwtManager *auth.JWTManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var tokenString string

		// Priority 1: Cookie (more secure for web clients)
		if cookieToken, err := c.Cookie("access_token"); err == nil && cookieToken != "" {
			tokenString = cookieToken
		} else {
			// Priority 2: Authorization header (for mobile apps and API clients)
			authHeader := c.GetHeader("Authorization")
			if authHeader != "" {
				parts := strings.SplitN(authHeader, " ", 2)
				if len(parts) == 2 && parts[0] == "Bearer" {
					tokenString = parts[1]
				}
			}
		}

		if tokenString == "" {
			response.Unauthorized(c, "Authentication required")
			c.Abort()
			return
		}

		claims, err := jwtManager.ValidateToken(tokenString)
		if err != nil {
			response.Unauthorized(c, "Invalid or expired token")
			c.Abort()
			return
		}

		// Set user information in context
		c.Set("user_id", claims.UserID.String())
		c.Set("user_email", claims.Email)
		c.Set("user_role", claims.Role)
		c.Set("claims", claims)

		c.Next()
	}
}

// RoleMiddleware checks if user has required role
func RoleMiddleware(allowedRoles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		role, exists := c.Get("user_role")
		if !exists {
			response.Forbidden(c, "User role not found")
			c.Abort()
			return
		}

		userRole, ok := role.(string)
		if !ok {
			response.Forbidden(c, "Invalid user role type")
			c.Abort()
			return
		}
		for _, allowedRole := range allowedRoles {
			if userRole == allowedRole {
				c.Next()
				return
			}
		}

		response.Forbidden(c, "Insufficient permissions")
		c.Abort()
	}
}

// RequireAdmin is a convenience middleware that checks for admin role
// SECURITY: This middleware requires AuthMiddleware to be called first, which performs
// cryptographic signature verification. Never trust role claims from unverified tokens.
func RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		var userRole string

		// Check for role from verified claims set by AuthMiddleware
		// AuthMiddleware MUST be called before this middleware to ensure token verification
		if claims, exists := c.Get("claims"); exists {
			if authClaims, ok := claims.(*auth.Claims); ok {
				userRole = authClaims.Role
			} else if claimsMap, ok := claims.(map[string]interface{}); ok {
				if role, ok := claimsMap["role"].(string); ok {
					userRole = role
				}
			}
		}

		// Fallback to user_role set by AuthMiddleware
		if userRole == "" {
			if role, exists := c.Get("user_role"); exists {
				if r, ok := role.(string); ok {
					userRole = r
				}
			}
		}

		// SECURITY: If no role found, the request was not authenticated
		// Do NOT attempt to parse tokens here - that's AuthMiddleware's job
		if userRole == "" {
			response.Unauthorized(c, "Authentication required - use AuthMiddleware before RequireAdmin")
			c.Abort()
			return
		}

		if userRole != "admin" && userRole != "super_admin" {
			response.Forbidden(c, "Admin access required")
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireAdminWithJWT is a standalone admin middleware that performs its own JWT verification
// Use this when you cannot chain AuthMiddleware before RequireAdmin
func RequireAdminWithJWT(jwtManager *auth.JWTManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var tokenString string

		// Try cookie first (more secure for web clients)
		if cookieToken, err := c.Cookie("access_token"); err == nil && cookieToken != "" {
			tokenString = cookieToken
		} else {
			// Fall back to Authorization header
			authHeader := c.GetHeader("Authorization")
			if authHeader != "" {
				parts := strings.SplitN(authHeader, " ", 2)
				if len(parts) == 2 && parts[0] == "Bearer" {
					tokenString = parts[1]
				}
			}
		}

		if tokenString == "" {
			response.Unauthorized(c, "Authentication required")
			c.Abort()
			return
		}

		// SECURITY: Always verify token signature before trusting claims
		claims, err := jwtManager.ValidateToken(tokenString)
		if err != nil {
			response.Unauthorized(c, "Invalid or expired token")
			c.Abort()
			return
		}

		if claims.Role != "admin" && claims.Role != "super_admin" {
			response.Forbidden(c, "Admin access required")
			c.Abort()
			return
		}

		// Set verified claims in context for downstream handlers
		c.Set("user_id", claims.UserID.String())
		c.Set("user_email", claims.Email)
		c.Set("user_role", claims.Role)
		c.Set("claims", claims)

		c.Next()
	}
}
