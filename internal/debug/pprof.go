package debug

import (
	"log/slog"
	"net/http"
	_ "net/http/pprof" //nolint:gosec // pprof is gated by PPROF_ENABLED env var
)

// StartPprof starts a pprof HTTP server on the given port.
// This should only be called when PPROF_ENABLED=true.
// The server listens on a separate internal port and is NOT exposed through Caddy.
func StartPprof(port string, logger *slog.Logger) {
	addr := "127.0.0.1:" + port
	logger.Info("starting pprof server", "addr", addr)
	go func() {
		if err := http.ListenAndServe(addr, nil); err != nil { //nolint:gosec // internal debug server
			logger.Error("pprof server error", "error", err)
		}
	}()
}
