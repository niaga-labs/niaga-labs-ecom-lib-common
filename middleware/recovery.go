package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
	"github.com/niaga-labs/niaga-labs-ecom-lib-common/response"
	"go.uber.org/zap"
)

// RecoveryMiddleware recovers from panics
func RecoveryMiddleware(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				stackTrace := string(debug.Stack())

				logger.Error("Panic recovered",
					zap.Any("error", err),
					zap.String("stack", stackTrace),
					zap.String("path", c.Request.URL.Path),
					zap.String("method", c.Request.Method),
				)

				response.Error(c, http.StatusInternalServerError, "INTERNAL_SERVER_ERROR",
					fmt.Sprintf("Internal server error: %v", err), nil)
				c.Abort()
			}
		}()
		c.Next()
	}
}
