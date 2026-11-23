package web

import (
	"net"
	"net/http"
)

// ClientIP sets r.RemoteAddr to the real client IP, so that logging and the
// sender_ip stored on messages see the client rather than the proxy.
//
// With trustCloudflare, the IP comes from the CF-Connecting-IP header that
// Cloudflare sets. A missing or unparsable header leaves RemoteAddr unchanged.
// Without it, RemoteAddr (the TCP peer) is kept as is.
//
// Only enable trustCloudflare when the server cannot be reached except through
// Cloudflare (Cloudflare Tunnel, or a firewall allowing only Cloudflare's IP
// ranges): the header is plain client input, so a direct connection could set
// it to anything.
func ClientIP(trustCloudflare bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if trustCloudflare {
				if ip := net.ParseIP(r.Header.Get("CF-Connecting-IP")); ip != nil {
					r.RemoteAddr = net.JoinHostPort(ip.String(), "0")
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
