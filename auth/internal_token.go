package auth

import (
	"crypto/subtle"
	"fmt"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/niaga-labs/niaga-labs-ecom-lib-common/response"
)

// Service-to-service authentication for the /internal/* routes.
//
// Those routes reserve, deduct and restock inventory, reserve flash-sale
// allocations, approve agent commissions and create marketplace orders. Until
// NIAGA-114 not one of them checked anything: service-inventory's block carried
// the comment "should be protected by internal network/service mesh in
// production" and service-order's said "No auth required - called by marketplace
// service". There is no service mesh, and nginx proxies these paths, so anyone
// who could reach the gateway could move stock or invent an order.
//
// This is deliberately a single shared token rather than the database-backed
// APIKeyMiddleware in this package (which no service uses): callers are our own
// services on a private network, and a token they can read from the environment
// is the smallest thing that closes the hole. Per-service keys with scopes are a
// later question, not a blocker for this one.

const (
	// InternalTokenHeader carries the shared service token.
	InternalTokenHeader = "X-Internal-Token"

	// InternalTokenEnvVar is where every service reads it from.
	InternalTokenEnvVar = "INTERNAL_API_TOKEN"

	// DevInternalToken is the value the SERVICE .env.example files ship, and the
	// only value this package will invent for you. It is a placeholder by design
	// and is refused outside development.
	DevInternalToken = "dev-internal-token" // secret-scan: allow

	// PlaceholderInternalToken is the value infra-platform/.env.example ships —
	// the compose stack uses a different placeholder convention from the service
	// .env.example files, and for a while the guard below knew about only one of
	// them (NIAGA-216). Copying that file and running docker compose up without
	// editing this line is the shortest path to a production-mode stack on a
	// token anyone can read off GitHub.
	PlaceholderInternalToken = "CHANGE_ME_GENERATE_WITH_openssl_rand_base64_32" // secret-scan: allow

	// PlaceholderPrefix is the convention infra-platform/.env.example uses for
	// every value the operator must replace: POSTGRES_PASSWORD, JWT_SECRET,
	// MARKETPLACE_ENCRYPTION_KEY, MINIO_ROOT_PASSWORD and the token above. It is
	// matched as a case-insensitive prefix so a guard on any of the others gets
	// the same rule for free.
	PlaceholderPrefix = "CHANGE_ME"

	// MinInternalTokenLength is the shortest token accepted outside development.
	// Every .env.example tells the operator to run `openssl rand -base64 32`,
	// which produces 44 characters; nothing generated for this purpose is short.
	// The floor exists because a list of known placeholders only ever catches the
	// ones somebody has already been bitten by.
	MinInternalTokenLength = 24
)

// InternalToken returns middleware requiring InternalTokenHeader to equal
// expected, compared in constant time.
//
// An empty expected token authenticates nobody. That matters: if it accepted
// anything when unconfigured, a service that failed to read its environment
// would silently serve its internal routes to the world — the same class of
// failure as the nil RBAC middleware in NIAGA-170.
func InternalToken(expected string) gin.HandlerFunc {
	expectedBytes := []byte(expected)

	return func(c *gin.Context) {
		if len(expectedBytes) == 0 {
			response.Unauthorized(c, "Internal API is not configured on this service")
			c.Abort()
			return
		}

		presented := c.GetHeader(InternalTokenHeader)
		if presented == "" {
			response.Unauthorized(c, "Internal service token required")
			c.Abort()
			return
		}

		if subtle.ConstantTimeCompare([]byte(presented), expectedBytes) != 1 {
			response.Unauthorized(c, "Invalid internal service token")
			c.Abort()
			return
		}

		c.Next()
	}
}

// IsDevEnv reports whether appEnv is one of the environments where falling back
// to DevInternalToken is acceptable. Anything unrecognised counts as production,
// because guessing wrong in that direction is the safe way to be wrong.
func IsDevEnv(appEnv string) bool {
	switch strings.ToLower(strings.TrimSpace(appEnv)) {
	case "", "dev", "development", "local", "test":
		return true
	default:
		return false
	}
}

// ResolveInternalToken reads INTERNAL_API_TOKEN.
//
// In development it falls back to DevInternalToken so a fresh clone runs with no
// setup. Anywhere else it refuses, in this order, an empty value, either of the
// two published placeholders, any other CHANGE_ME_ value, and anything under
// MinInternalTokenLength — and the caller is expected to refuse to start rather
// than serve these routes with a token anyone can read off GitHub.
//
// The order matters only for the error text: DevInternalToken is 18 characters
// and would trip the length floor too, but the specific message is the one that
// tells the operator which file to look in.
func ResolveInternalToken(appEnv string) (string, error) {
	token := strings.TrimSpace(os.Getenv(InternalTokenEnvVar))

	if IsDevEnv(appEnv) {
		if token == "" {
			return DevInternalToken, nil
		}
		return token, nil
	}

	if token == "" {
		return "", fmt.Errorf("%s is required when APP_ENV=%q: the internal routes "+
			"reserve stock, approve commissions and create orders", InternalTokenEnvVar, appEnv)
	}
	if token == DevInternalToken {
		return "", fmt.Errorf("%s is set to the development placeholder while APP_ENV=%q; "+
			"that value is published in every .env.example", InternalTokenEnvVar, appEnv)
	}
	if strings.HasPrefix(strings.ToUpper(token), PlaceholderPrefix) {
		return "", fmt.Errorf("%s still holds a %s_ placeholder while APP_ENV=%q; "+
			"infra-platform/.env.example ships %s=%s and docker compose passes it through "+
			"unedited, so it is as published as any other value in the repo",
			InternalTokenEnvVar, PlaceholderPrefix, appEnv, InternalTokenEnvVar, PlaceholderInternalToken)
	}
	if len(token) < MinInternalTokenLength {
		return "", fmt.Errorf("%s is %d characters while APP_ENV=%q; the internal routes require "+
			"at least %d, because anything shorter is a placeholder somebody typed rather than a "+
			"secret something generated — use `openssl rand -base64 32`",
			InternalTokenEnvVar, len(token), appEnv, MinInternalTokenLength)
	}

	return token, nil
}

// MustResolveInternalToken is ResolveInternalToken for a main() that should not
// start without one. Failing loudly at boot beats serving stock movements to
// anyone who asks.
func MustResolveInternalToken(appEnv string) string {
	token, err := ResolveInternalToken(appEnv)
	if err != nil {
		panic("SECURITY: " + err.Error())
	}
	return token
}
