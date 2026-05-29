package proxmox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

func (c *Client) GetHAResources(ctx context.Context) ([]HAResource, error) {
	var resources []HAResource
	if err := c.do(ctx, "/cluster/ha/resources", &resources); err != nil {
		return nil, fmt.Errorf("get HA resources: %w", err)
	}
	return resources, nil
}
func (c *Client) SetHAResourceState(ctx context.Context, sid string, state string) error {
	path := "/cluster/ha/resources/" + url.PathEscape(sid)
	form := url.Values{}
	form.Set("state", state)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("set HA resource %s state to %s: %w", sid, state, err)
	}
	return nil
}

// ArmHA re-arms the HA stack cluster-wide after it was disarmed (PVE 9.2+),
// restoring automatic fencing/recovery. POST /cluster/ha/status/arm-ha.
func (c *Client) ArmHA(ctx context.Context) error {
	if err := c.doPost(ctx, "/cluster/ha/status/arm-ha", url.Values{}, nil); err != nil {
		return fmt.Errorf("arm HA: %w", err)
	}
	return nil
}

// DisarmHA disarms the HA stack cluster-wide for planned maintenance (PVE 9.2+),
// releasing all watchdogs so controlled actions aren't treated as failures.
// resourceMode is "freeze" (lock services in place) or "ignore" (suspend HA
// tracking so services can be managed manually). POST /cluster/ha/status/disarm-ha.
func (c *Client) DisarmHA(ctx context.Context, resourceMode string) error {
	form := url.Values{}
	form.Set("resource-mode", resourceMode)
	if err := c.doPost(ctx, "/cluster/ha/status/disarm-ha", form, nil); err != nil {
		return fmt.Errorf("disarm HA (%s): %w", resourceMode, err)
	}
	return nil
}
func (c *Client) GetHAGroups(ctx context.Context) ([]HAGroup, error) {
	var groups []HAGroup
	if err := c.do(ctx, "/cluster/ha/groups", &groups); err != nil {
		return nil, fmt.Errorf("get HA groups: %w", err)
	}
	return groups, nil
}
func (c *Client) GetHARules(ctx context.Context) ([]HARuleEntry, error) {
	var rules []HARuleEntry
	if err := c.do(ctx, "/cluster/ha/rules", &rules); err != nil {
		return nil, fmt.Errorf("get HA rules: %w", err)
	}
	return rules, nil
}
func (c *Client) CreateHARule(ctx context.Context, ruleType string, params CreateHARuleParams) error {
	form := url.Values{}
	form.Set("rule", params.Rule)
	form.Set("type", ruleType)
	form.Set("resources", params.Resources)
	if params.Nodes != "" {
		form.Set("nodes", params.Nodes)
	}
	if params.Strict != 0 {
		form.Set("strict", strconv.Itoa(params.Strict))
	}
	if params.Affinity != "" {
		form.Set("affinity", params.Affinity)
	}
	if params.Comment != "" {
		form.Set("comment", params.Comment)
	}
	if err := c.doPost(ctx, "/cluster/ha/rules", form, nil); err != nil {
		return fmt.Errorf("create HA rule %q: %w", params.Rule, err)
	}
	return nil
}
func (c *Client) UpdateHARule(ctx context.Context, ruleID string, ruleType string, params UpdateHARuleParams) error {
	path := "/cluster/ha/rules/" + url.PathEscape(ruleID)
	form := url.Values{}
	form.Set("type", ruleType)
	if params.Resources != nil {
		form.Set("resources", *params.Resources)
	}
	if params.Nodes != nil {
		form.Set("nodes", *params.Nodes)
	}
	if params.Strict != nil {
		form.Set("strict", strconv.Itoa(*params.Strict))
	}
	if params.Affinity != nil {
		form.Set("affinity", *params.Affinity)
	}
	if params.Comment != nil {
		form.Set("comment", *params.Comment)
	}
	if params.Disable != nil {
		form.Set("disable", strconv.Itoa(*params.Disable))
	}
	if params.Digest != "" {
		form.Set("digest", params.Digest)
	}
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update HA rule %s: %w", ruleID, err)
	}
	return nil
}
func (c *Client) SetHARuleDisabled(ctx context.Context, ruleID string, ruleType string, disabled bool) error {
	disable := 0
	if disabled {
		disable = 1
	}
	return c.UpdateHARule(ctx, ruleID, ruleType, UpdateHARuleParams{Disable: &disable})
}
func (c *Client) DeleteHARule(ctx context.Context, ruleID string) error {
	path := "/cluster/ha/rules/" + url.PathEscape(ruleID)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete HA rule %q: %w", ruleID, err)
	}
	return nil
}
func (c *Client) CreateHAResource(ctx context.Context, params CreateHAResourceParams) error {
	form := url.Values{}
	form.Set("sid", params.SID)
	if params.State != "" {
		form.Set("state", params.State)
	}
	if params.Group != "" {
		form.Set("group", params.Group)
	}
	if params.MaxRestart > 0 {
		form.Set("max_restart", strconv.Itoa(params.MaxRestart))
	}
	if params.MaxRelocate > 0 {
		form.Set("max_relocate", strconv.Itoa(params.MaxRelocate))
	}
	if params.Comment != "" {
		form.Set("comment", params.Comment)
	}
	if params.Failback != nil {
		form.Set("failback", strconv.Itoa(*params.Failback))
	}
	// Replace %3A back to literal colon — Proxmox validates SID before URL-decoding.
	body := strings.ReplaceAll(form.Encode(), "%3A", ":")
	if err := c.doPostRaw(ctx, "/cluster/ha/resources", body, nil); err != nil {
		return fmt.Errorf("create HA resource %s: %w", params.SID, err)
	}
	return nil
}
func (c *Client) GetHAResource(ctx context.Context, sid string) (*HAResource, error) {
	// Use raw SID (e.g. "vm:100") — Proxmox rejects percent-encoded colons in HA SID paths.
	path := "/cluster/ha/resources/" + sid
	var res HAResource
	if err := c.do(ctx, path, &res); err != nil {
		return nil, fmt.Errorf("get HA resource %s: %w", sid, err)
	}
	return &res, nil
}
func (c *Client) UpdateHAResource(ctx context.Context, sid string, params UpdateHAResourceParams) error {
	form := url.Values{}
	if params.State != nil {
		form.Set("state", *params.State)
	}
	if params.Group != nil {
		form.Set("group", *params.Group)
	}
	if params.MaxRestart != nil {
		form.Set("max_restart", strconv.Itoa(*params.MaxRestart))
	}
	if params.MaxRelocate != nil {
		form.Set("max_relocate", strconv.Itoa(*params.MaxRelocate))
	}
	if params.Comment != nil {
		form.Set("comment", *params.Comment)
	}
	if params.Failback != nil {
		form.Set("failback", strconv.Itoa(*params.Failback))
	}
	if params.Digest != "" {
		form.Set("digest", params.Digest)
	}
	// Use raw SID — Proxmox rejects percent-encoded colons in HA SID paths.
	path := "/cluster/ha/resources/" + sid
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update HA resource %s: %w", sid, err)
	}
	return nil
}
func (c *Client) DeleteHAResource(ctx context.Context, sid string) error {
	// Use raw SID — Proxmox rejects percent-encoded colons in HA SID paths.
	path := "/cluster/ha/resources/" + sid
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete HA resource %s: %w", sid, err)
	}
	return nil
}
func (c *Client) CreateHAGroup(ctx context.Context, params CreateHAGroupParams) error {
	form := url.Values{}
	form.Set("group", params.Group)
	form.Set("nodes", params.Nodes)
	if params.Restricted != 0 {
		form.Set("restricted", strconv.Itoa(params.Restricted))
	}
	if params.NoFailback != 0 {
		form.Set("nofailback", strconv.Itoa(params.NoFailback))
	}
	if params.Comment != "" {
		form.Set("comment", params.Comment)
	}
	if err := c.doPost(ctx, "/cluster/ha/groups", form, nil); err != nil {
		return fmt.Errorf("create HA group %s: %w", params.Group, err)
	}
	return nil
}
func (c *Client) GetHAGroup(ctx context.Context, group string) (*HAGroup, error) {
	path := "/cluster/ha/groups/" + url.PathEscape(group)
	var g HAGroup
	if err := c.do(ctx, path, &g); err != nil {
		return nil, fmt.Errorf("get HA group %s: %w", group, err)
	}
	return &g, nil
}
func (c *Client) UpdateHAGroup(ctx context.Context, group string, params UpdateHAGroupParams) error {
	form := url.Values{}
	if params.Nodes != nil {
		form.Set("nodes", *params.Nodes)
	}
	if params.Restricted != nil {
		form.Set("restricted", strconv.Itoa(*params.Restricted))
	}
	if params.NoFailback != nil {
		form.Set("nofailback", strconv.Itoa(*params.NoFailback))
	}
	if params.Comment != nil {
		form.Set("comment", *params.Comment)
	}
	if params.Digest != "" {
		form.Set("digest", params.Digest)
	}
	path := "/cluster/ha/groups/" + url.PathEscape(group)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update HA group %s: %w", group, err)
	}
	return nil
}
func (c *Client) DeleteHAGroup(ctx context.Context, group string) error {
	path := "/cluster/ha/groups/" + url.PathEscape(group)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete HA group %s: %w", group, err)
	}
	return nil
}
func (c *Client) GetHAStatus(ctx context.Context) ([]HAStatusEntry, error) {
	var entries []HAStatusEntry
	if err := c.do(ctx, "/cluster/ha/status/current", &entries); err != nil {
		return nil, fmt.Errorf("get HA status: %w", err)
	}
	return entries, nil
}
func (c *Client) GetHAManagerStatus(ctx context.Context) (map[string]json.RawMessage, error) {
	var status map[string]json.RawMessage
	if err := c.do(ctx, "/cluster/ha/status/manager_status", &status); err != nil {
		return nil, fmt.Errorf("get HA manager status: %w", err)
	}
	return status, nil
}
