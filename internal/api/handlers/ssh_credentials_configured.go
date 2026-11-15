package handlers

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
)

// sshConfiguredResponse is the whole answer: a boolean, never the username,
// the port, a host or the key, so a caller who may only RUN what needs the
// credential learns that it exists and nothing about it.
type sshConfiguredResponse struct {
	Configured bool `json:"configured"`
}

// SSHCredentialsConfigured handles GET /api/v1/clusters/:cluster_id/ssh-credentials/configured.
// A failed read is a 500, not "no": the SPA disables actions on a no.
func (h *RollingUpdateHandler) SSHCredentialsConfigured(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	configured, err := h.queries.HasClusterSSHCredentials(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to check SSH credentials")
	}
	return c.JSON(sshConfiguredResponse{Configured: configured})
}
