package middleware

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"net/http/httptest"
	"os"
	"testing"
)

func TestTrustedProxyClientIP(t *testing.T) {
	cases := []struct{ name, proxies, remote, forwarded, real, want string }{
		{"public socket ignores forged headers", DefaultTrustedProxies, "203.0.113.8:1234", "1.2.3.4", "5.6.7.8", "203.0.113.8"},
		{"private gateway forwards caller", DefaultTrustedProxies, "172.18.0.3:1234", "1.2.3.4", "5.6.7.8", "1.2.3.4"},
		{"chain stops at right untrusted hop", DefaultTrustedProxies, "172.18.0.3:1234", "1.2.3.4, 198.51.100.8", "", "198.51.100.8"},
		{"IPv6 gateway", DefaultTrustedProxies, "[::1]:1234", "2001:db8::8", "", "2001:db8::8"},
		{"disabled mode ignores local headers", "", "127.0.0.1:1234", "1.2.3.4", "5.6.7.8", "127.0.0.1"},
		{"narrow range excludes private peer", "172.18.0.3/32", "172.18.0.4:1234", "1.2.3.4", "", "172.18.0.4"},
		{"malformed header ignored", DefaultTrustedProxies, "172.18.0.3:1234", "garbage", "1.2.3.4", "172.18.0.3"},
		{"real-IP is not alternate trust channel", DefaultTrustedProxies, "172.18.0.3:1234", "", "1.2.3.4", "172.18.0.3"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TRUSTED_PROXIES", tt.proxies)
			r := gin.New()
			r.TrustedPlatform = "X-Platform-IP"
			r.AppEngine = true
			if err := ConfigureTrustedProxies(r, zap.NewNop()); err != nil {
				t.Fatal(err)
			}
			r.GET("/", func(c *gin.Context) { c.String(200, c.ClientIP()) })
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remote
			req.Header.Set("X-Forwarded-For", tt.forwarded)
			req.Header.Set("X-Real-IP", tt.real)
			req.Header.Set("X-Platform-IP", "9.9.9.9")
			req.Header.Set("X-Appengine-Remote-Addr", "8.8.8.8")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Body.String() != tt.want {
				t.Fatalf("got %q want %q", rec.Body.String(), tt.want)
			}
		})
	}
}

func TestInvalidProxyConfigFailsClosed(t *testing.T) {
	for _, value := range []string{"bad", "127.0.0.1,", "0.0.0.0/0", "::/0", "203.0.113.2", "10.0.0.0/7", "172.0.0.0/8", "fc00::/6"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("TRUSTED_PROXIES", value)
			r := gin.New()
			if err := ConfigureTrustedProxies(r, zap.NewNop()); err == nil {
				t.Fatal("expected rejected configuration")
			}
			r.GET("/", func(c *gin.Context) { c.String(200, c.ClientIP()) })
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = "127.0.0.1:1234"
			req.Header.Set("X-Forwarded-For", "1.2.3.4")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Body.String() != "127.0.0.1" {
				t.Fatal("invalid config retained header trust")
			}
		})
	}
}

func TestProxyDefaultsAndBootLog(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", "")
	if err := os.Unsetenv("TRUSTED_PROXIES"); err != nil {
		t.Fatal(err)
	}
	core, logs := observer.New(zap.InfoLevel)
	if err := ConfigureTrustedProxies(gin.New(), zap.New(core)); err != nil {
		t.Fatal(err)
	}
	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("want one boot log, got %d", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["forwarded_headers_enabled"] != true {
		t.Fatal("default mode not logged")
	}
	if len(fields["trusted_proxies"].([]interface{})) != 6 {
		t.Fatal("resolved list not logged")
	}
	t.Setenv("TRUSTED_PROXIES", "")
	if err := ConfigureTrustedProxies(gin.New(), zap.New(core)); err != nil {
		t.Fatal(err)
	}
	if logs.All()[1].ContextMap()["forwarded_headers_enabled"] != false {
		t.Fatal("disabled mode not logged")
	}
}
