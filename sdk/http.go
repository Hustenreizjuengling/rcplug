package sdk

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// APIHeader is the header every state-changing request must carry. Browsers
// refuse to add custom headers to cross-site requests without a CORS
// preflight (which rcplug never grants), so a foreign web page cannot drive
// the device through a visitor's browser.
const APIHeader = "X-Rcplug"

// CSRF rejects non-safe methods without the APIHeader.
func CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if r.Header.Get(APIHeader) == "" {
				WriteJSON(w, http.StatusForbidden, map[string]string{"error": "missing " + APIHeader + " header", "code": "csrf"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// SignalContext returns a context that is cancelled on SIGINT or SIGTERM —
// the host stops plugins with SIGINT and force-kills them 3 s later.
func SignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// Serve serves h (wrapped in CSRF) on addr until ctx is cancelled, then shuts
// down gracefully. Long-lived requests (SSE, streams) see their request
// context cancelled when ctx ends, so shutdown does not wait for them.
func Serve(ctx context.Context, addr string, h http.Handler) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return ServeListener(ctx, ln, h)
}

// ServeListener is Serve on an existing listener.
func ServeListener(ctx context.Context, ln net.Listener, h http.Handler) error {
	srv := &http.Server{
		Handler:           CSRF(h),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := srv.Shutdown(sctx)
	if serr := <-done; serr != nil && !errors.Is(serr, http.ErrServerClosed) {
		return serr
	}
	return err
}
