package handlers

import (
	"net"
	"net/http"
	"net/netip"

	"github.com/go-chi/chi/v5/middleware"
)

// ClientIP records the client address for the request log, believing
// X-Forwarded-For only when the connection comes from a trusted proxy. Read
// it with middleware.GetClientIP; middleware.Logger already does.
//
// chi's ClientIPFromXFF on its own is not enough: it never looks at who
// opened the connection, so a client reaching the port directly could name
// any address it liked. Here the header is consulted only for a trusted peer,
// and then walked from the right, skipping trusted hops, so a value the
// client put in front of the proxy's entry is never taken. Everyone else is
// logged by the address of the connection. True-Client-IP and X-Real-IP are
// never read.
//
// A trusted peer that sends no usable header gets no client IP, and the log
// falls back to RemoteAddr: the proxy, not a guess.
func ClientIP(trusted []netip.Prefix) func(http.Handler) http.Handler {
	cidrs := make([]string, len(trusted))
	for i, p := range trusted {
		cidrs[i] = p.String()
	}
	return func(next http.Handler) http.Handler {
		fromXFF := middleware.ClientIPFromXFF(cidrs...)(next)
		fromPeer := middleware.ClientIPFromRemoteAddr(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if peerTrusted(r.RemoteAddr, trusted) {
				fromXFF.ServeHTTP(w, r)
				return
			}
			fromPeer.ServeHTTP(w, r)
		})
	}
}

func peerTrusted(remoteAddr string, trusted []netip.Prefix) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	// A dual-stack listener reports IPv4 peers as ::ffff:a.b.c.d, which an
	// IPv4 prefix does not contain until unmapped.
	addr = addr.Unmap()
	for _, p := range trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
