package api

import (
	"runtime"

	"github.com/gofiber/fiber/v2"
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

func (s *Server) handleVersion(c *fiber.Ctx) error {
	return c.JSON(versionResponse{
		Version:   Version,
		Commit:    Commit,
		BuildTime: BuildTime,
		GoVersion: runtime.Version(),
	})
}

func (s *Server) handleHealthz(c *fiber.Ctx) error {
	if s.db != nil {
		if err := s.db.Ping(c.Context()); err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"status": "unhealthy",
				"error":  err.Error(),
			})
		}
	}
	return c.JSON(fiber.Map{"status": "ok"})
}
