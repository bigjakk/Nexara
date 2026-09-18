package api

import (
	"log/slog"
	"runtime"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
)

// Version information set via ldflags at build time.
var (
	Version   = "dev"
	Commit    = "none"
	BuildTime = "unknown"
)

type versionResponse struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
	GoVersion string `json:"go_version"`
}

// handleVersion is declared in internal/api/registry_version.go. It is the one
// registry handler that is a Server method rather than a handlers-package one,
// because the values it reports are package api's own ldflags variables.
func (s *Server) handleVersion(c fiber.Ctx, _ *apischema.Params) error {
	return c.JSON(versionResponse{
		Version:   Version,
		Commit:    Commit,
		BuildTime: BuildTime,
		GoVersion: runtime.Version(),
	})
}

func (s *Server) handleHealthz(c fiber.Ctx) error {
	if s.db != nil {
		if err := s.db.Ping(c.Context()); err != nil {
			// Log the detail server-side; don't disclose DB host/driver internals
			// to unauthenticated callers — the probe only needs the status code.
			slog.Warn("healthz: database ping failed", "error", err)
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"status": "unhealthy",
			})
		}
	}
	return c.JSON(fiber.Map{"status": "ok"})
}
