package handlers

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
	"github.com/bigjakk/nexara/internal/pvefield"
)

// ClusterOptionsHandler handles cluster options and datacenter config endpoints.
type ClusterOptionsHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewClusterOptionsHandler creates a new ClusterOptionsHandler.
func NewClusterOptionsHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *ClusterOptionsHandler {
	return &ClusterOptionsHandler{queries: queries, encryptionKey: encryptionKey, eventPub: eventPub}
}

func (h *ClusterOptionsHandler) createProxmoxClient(c fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
	return CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
}

// clusterOptionsAudit is the details of an options write: the NAMES of the
// options it set and of the ones it cleared, never their values. http_proxy can
// carry credentials and view:audit is granted to every Viewer; the names are
// what a History row needs to say what changed. action is the key these rows
// have always carried. Taken from the typed params rather than a list of keys,
// so an option added to the struct is covered without anyone remembering to.
// keep, when not nil, keeps the names to those it holds: what the write moved
// (movedFields), as the options page sends its whole form.
func clusterOptionsAudit(action string, req proxmox.UpdateClusterOptionsParams, keep func(string) bool) map[string]any {
	if keep == nil {
		keep = func(string) bool { return true }
	}
	settings := []string{}
	v := reflect.ValueOf(req)
	for i := 0; i < v.NumField(); i++ {
		if f := v.Field(i); f.Kind() == reflect.Pointer && !f.IsNil() {
			if name, _, _ := strings.Cut(v.Type().Field(i).Tag.Get("json"), ","); keep(name) {
				settings = append(settings, name)
			}
		}
	}
	cleared := []string{}
	for _, name := range strings.Split(req.Delete, ",") {
		if name = strings.TrimSpace(name); name != "" && keep(name) {
			cleared = append(cleared, name)
		}
	}
	return map[string]any{"action": action, "settings": settings, "cleared": cleared}
}

// GetOptions handles GET /clusters/:cluster_id/options.
func (h *ClusterOptionsHandler) GetOptions(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	opts, err := pxClient.GetClusterOptions(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(opts)
}

// UpdateOptions handles PUT /clusters/:cluster_id/options.
//
// Every property is read as a POINTER, so that omitting a key means "do
// not send this property to Proxmox" and sending it empty means "send it
// empty" — the distinction the bound struct expressed with *string, and
// which the OptX reads below now carry in their supplied flag (a flag no
// declared default sets — apischema.Property.Default). The names are the
// wire names Proxmox itself uses, hyphens and all.
func (h *ClusterOptionsHandler) UpdateOptions(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	req := proxmox.UpdateClusterOptionsParams{
		Console:        optStringPtr(p.OptString("console")),
		Keyboard:       optStringPtr(p.OptString("keyboard")),
		Language:       optStringPtr(p.OptString("language")),
		EmailFrom:      optStringPtr(p.OptString("email_from")),
		HTTPProxy:      optStringPtr(p.OptString("http_proxy")),
		MacPrefix:      optStringPtr(p.OptString("mac_prefix")),
		Migration:      optStringPtr(p.OptString("migration")),
		MigrationType:  optStringPtr(p.OptString("migration_type")),
		BWLimit:        optStringPtr(p.OptString("bwlimit")),
		NextID:         optStringPtr(p.OptString("next-id")),
		HA:             optStringPtr(p.OptString("ha")),
		Fencing:        optStringPtr(p.OptString("fencing")),
		CRS:            optStringPtr(p.OptString("crs")),
		MaxWorkers:     optIntPtr(p.OptInt("max_workers")),
		Description:    optStringPtr(p.OptString("description")),
		RegisteredTags: optStringPtr(p.OptString("registered-tags")),
		UserTagAccess:  optStringPtr(p.OptString("user-tag-access")),
		TagStyle:       optStringPtr(p.OptString("tag-style")),
		Delete:         p.String("delete"),
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	read := readBefore(c.Context(), pxClient)
	before := read(proxmox.ObserveClusterOptions(), nil)
	if err := pxClient.SetClusterOptions(c.Context(), req); err != nil {
		return mapProxmoxError(err)
	}
	moved, ok := movedFields(before, read(proxmox.ObserveClusterOptions(), nil), pvefield.ClusterOptions)
	var details map[string]any
	if ok {
		details = clusterOptionsAudit("update_options", req, func(name string) bool { return slices.Contains(moved, name) })
	} else {
		details = clusterOptionsAudit("update_options", req, nil)
		details["fields_unverified"] = true
	}
	audit, _ := json.Marshal(details)
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "cluster_options", clusterID.String(), "updated", audit)
	return c.JSON(fiber.Map{"status": "ok"})
}

// GetDescription handles GET /clusters/:cluster_id/description.
func (h *ClusterOptionsHandler) GetDescription(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	opts, err := pxClient.GetClusterOptions(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(fiber.Map{"description": opts.Description})
}

// UpdateDescription handles PUT /clusters/:cluster_id/description.
//
// The description is forwarded unconditionally rather than as a tri-state:
// this route exists to SET it, and an omitted key has always cleared it
// because the bound struct's zero value was an empty string whose address
// was taken regardless. The schema's Default of "" states that rather than
// changing it.
func (h *ClusterOptionsHandler) UpdateDescription(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	description := p.String("description")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	req := proxmox.UpdateClusterOptionsParams{Description: &description}
	if err := pxClient.SetClusterOptions(c.Context(), req); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(clusterOptionsAudit("update_description", req, nil))
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "cluster_options", clusterID.String(), "description_updated", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// GetTags handles GET /clusters/:cluster_id/tags.
func (h *ClusterOptionsHandler) GetTags(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	opts, err := pxClient.GetClusterOptions(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(fiber.Map{
		"registered_tags": opts.RegisteredTags,
		"user_tag_access": opts.UserTagAccess,
		"tag_style":       opts.TagStyle,
	})
}

// UpdateTags handles PUT /clusters/:cluster_id/tags.
//
// The three parameters keep their UNDERSCORED wire names, which are not
// the hyphenated ones Proxmox uses and the options endpoint forwards. The
// client translates; renaming them here would break every existing caller.
func (h *ClusterOptionsHandler) UpdateTags(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	req := proxmox.UpdateClusterOptionsParams{
		RegisteredTags: optStringPtr(p.OptString("registered_tags")),
		UserTagAccess:  optStringPtr(p.OptString("user_tag_access")),
		TagStyle:       optStringPtr(p.OptString("tag_style")),
	}
	if err := pxClient.SetClusterOptions(c.Context(), req); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(clusterOptionsAudit("update_tags", req, nil))
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "cluster_options", clusterID.String(), "tags_updated", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// GetClusterConfig handles GET /clusters/:cluster_id/config.
func (h *ClusterOptionsHandler) GetClusterConfig(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	cfg, err := pxClient.GetClusterConfig(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(cfg)
}

// GetJoinInfo handles GET /clusters/:cluster_id/config/join.
func (h *ClusterOptionsHandler) GetJoinInfo(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	info, err := pxClient.GetClusterJoinInfo(c.Context())
	if err != nil {
		// A node that is not in a cluster is not a failure. PVE answers
		// 424 for it, which mapProxmoxError would render as a 502 and the
		// SPA as an error banner — hiding its own empty state for exactly
		// this case ("No join info available. This may be a standalone
		// node.", ClusterOptionsTab.tsx), which is gated on !isError.
		//
		// The two sibling routes under /config handle it the same way, in
		// the client; this one is handled here because GetClusterJoinInfo
		// is where they detect it, so it must keep returning the error.
		if proxmox.IsNotInClusterError(err) {
			return c.JSON(&proxmox.ClusterJoinInfo{})
		}
		return mapProxmoxError(err)
	}
	return c.JSON(info)
}

// ListCorosyncNodes handles GET /clusters/:cluster_id/config/nodes.
func (h *ClusterOptionsHandler) ListCorosyncNodes(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	nodes, err := pxClient.GetCorosyncNodes(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return RespondItems(c, nodes)
}
