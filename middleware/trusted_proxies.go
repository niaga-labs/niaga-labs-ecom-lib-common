package middleware

import (
	"fmt"
	"net/netip"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// DefaultTrustedProxies covers local gateways and private container networks.
// Deployments should narrow TRUSTED_PROXIES to their actual gateway addresses.
const DefaultTrustedProxies = "127.0.0.0/8,::1/128,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,fc00::/7"

// ConfigureTrustedProxies removes Gin's trust-all default before serving requests.
// Unset TRUSTED_PROXIES uses local/private ranges; explicitly empty disables header
// trust. Invalid or public ranges fail closed and must abort service startup.
func ConfigureTrustedProxies(router *gin.Engine, logger *zap.Logger) error {
	// Even callers accidentally ignoring an error must never retain Gin's default.
	if err := router.SetTrustedProxies(nil); err != nil {
		return fmt.Errorf("disable trusted proxies: %w", err)
	}
	router.TrustedPlatform = ""
	router.AppEngine = false
	router.RemoteIPHeaders = []string{"X-Forwarded-For"}
	router.ForwardedByClientIP = true
	value, set := os.LookupEnv("TRUSTED_PROXIES")
	if !set {
		value = DefaultTrustedProxies
	}
	proxies, err := privateProxyList(value)
	if err != nil {
		return err
	}
	if err := router.SetTrustedProxies(proxies); err != nil {
		return fmt.Errorf("configure trusted proxies: %w", err)
	}
	logger.Info("trusted proxy configuration", zap.Strings("trusted_proxies", proxies), zap.Bool("forwarded_headers_enabled", len(proxies) > 0))
	return nil
}

func privateProxyList(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	allowed := strings.Split(DefaultTrustedProxies, ",")
	proxies := make([]string, 0)
	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		prefix, err := netip.ParsePrefix(entry)
		if err != nil {
			ip, ipErr := netip.ParseAddr(entry)
			if ipErr != nil {
				return nil, fmt.Errorf("invalid TRUSTED_PROXIES entry %q", entry)
			}
			prefix = netip.PrefixFrom(ip, ip.BitLen())
		}
		prefix = prefix.Masked()
		private := false
		for _, rangeText := range allowed {
			boundary := netip.MustParsePrefix(rangeText)
			if boundary.Contains(prefix.Addr()) && prefix.Bits() >= boundary.Bits() {
				private = true
				break
			}
		}
		if !private {
			return nil, fmt.Errorf("TRUSTED_PROXIES entry %q must be contained in a loopback or private range", entry)
		}
		proxies = append(proxies, prefix.String())
	}
	return proxies, nil
}
