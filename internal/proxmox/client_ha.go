package proxmox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
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
	if err := validateHAConfigID("rule", ruleID); err != nil {
		return err
	}
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
		// Re-enabling has to unset the property, not send disable=0. This is
		// the canonical explanation; ha.go and the rolling orchestrator point
		// here rather than restating it.
		//
		// pve-ha-manager's update_rule runs `delete $param->{disable} if
		// !$param->{disable}` before it reads the config, so a falsy disable
		// never reaches the rule: PVE answers 200 and changes nothing. A rule
		// can be switched off and never back on, and the caller has no error
		// to notice — the rolling-update orchestrator's restore step looked
		// like it worked for exactly this reason.
		//
		// Unsetting is also the only correct representation. The HA manager
		// tests the flag with exists(), not truth, so a literal `disable 0`
		// left in /etc/pve/ha/rules.cfg would still read as disabled.
		//
		// `delete` is SectionConfig's generic unset list, comma-separated. It
		// must not carry a key that is also being set — delete_from_config
		// dies with "cannot set and delete property" — hence the else. A
		// second unsettable key has to join this one list rather than Set()
		// over it.
		if *params.Disable == 0 {
			form.Set("delete", "disable")
		} else {
			form.Set("disable", strconv.Itoa(*params.Disable))
		}
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
	if err := validateHAConfigID("rule", ruleID); err != nil {
		return err
	}
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
	// Sent whenever the caller chose a value, 0 included. Both counts are
	// `optional => 1, default => 1, minimum => 0` in pve-ha-manager
	// (src/PVE/HA/Resources.pm), and that default only ever reaches a key that
	// is ABSENT: the create call (src/PVE/API2/HA/Resources.pm) stores every
	// defined value through pve-common's SectionConfig check_config, 0 among
	// them, and checked_resources_config (src/PVE/HA/Config.pm) fills in 1
	// only `if !defined`. So an omitted key means 1 and a 0 means 0, which is
	// why these are pointers. They were ints sent only when `> 0`, which turned
	// every explicit 0 into a 1.
	if params.MaxRestart != nil {
		form.Set("max_restart", strconv.Itoa(*params.MaxRestart))
	}
	if params.MaxRelocate != nil {
		form.Set("max_relocate", strconv.Itoa(*params.MaxRelocate))
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

// haResourceIDPattern matches a Proxmox HA resource id: the guest's VMID with
// an optional type prefix — "vm:100", "ct:101", "100".
var haResourceIDPattern = regexp.MustCompile(`^(?:[a-z]{1,16}:)?\d{1,12}$`)

// validateHAResourceID checks an HA SID before it is interpolated into a request
// path. Like volume ids these go out literally rather than percent-encoded
// (Proxmox rejects an encoded colon here — see the call sites), so the pattern
// is the only thing keeping an operator-supplied SID from redirecting a request
// that carries the cluster's API token. It is deliberately strict: HA resources
// are VMs and containers, addressed by VMID.
func validateHAResourceID(sid string) error {
	if sid == "" {
		return fmt.Errorf("%w: HA resource id is required", ErrInvalidInput)
	}
	if !haResourceIDPattern.MatchString(sid) {
		return fmt.Errorf("%w: HA resource id %q should be a VMID, optionally prefixed (e.g. \"vm:100\")", ErrInvalidInput, sid)
	}
	return nil
}

// haConfigIDPattern matches a Proxmox HA group or rule id: a leading letter,
// then letters, digits, underscore and dash.
//
// It is the catalogue's `pve-configid-existing` rule verbatim
// (internal/api/apischema/catalogue.go), which is Proxmox's own $CONFIGID_RE
// from pve-common's JSONSchema.pm loosened by exactly one character — PVE
// demands two, this accepts one — so that an id created outside Nexara can
// never become unaddressable through a stricter rule of ours. Both an HA group
// id (pve-ha-group-id) and an HA rule id are declared `format: pve-configid`
// upstream, so the two share one pattern.
var haConfigIDPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

// validateHAConfigID checks an HA group or rule id before it is interpolated
// into a request path. kind is "group" or "rule", and appears in the message.
//
// This lives here, at the client, rather than in the handlers, because the
// handlers are not the only caller and the next one will not remember. Until
// this existed the five methods that interpolate one of these ids were safe
// only because the route declarations happened to carry a pattern that excluded
// "%", "." and "/" — haConfigIDParam in registry_ha.go, reused by the DRS
// delete route in registry_drs.go. That is a guard in the caller: it protects
// the callers that opt in and nothing else. The rolling orchestrator already
// reaches UpdateHARule through SetHARuleDisabled without passing any
// declaration at all.
//
// url.PathEscape is not a substitute, and the escaping it does is the reason it
// looks like one. It escapes "/" to %2F and leaves ".." untouched, and pveproxy
// decodes the escape BEFORE it routes (see validatePathSegment), so an escaped
// separator is a separator there. So "../../../nodes/pve-01/qemu/100" leaves
// here as "..%2F..%2F..%2Fnodes%2Fpve-01%2Fqemu%2F100". pveproxy would split it
// but not resolve the dots — the id becomes ".." and nothing below it routes —
// while a proxy in front of it that decodes %2F and removes dot segments would
// deliver a DELETE three levels up, on a guest, carrying the cluster's API
// token.
//
// The pattern rather than a separator ban, deliberately. validatePathSegment
// would stop the traversal — PathEscape re-encodes a literal "%" to %25, so
// unlike a volume id there is nothing here to smuggle an escape through — but
// it would leave the client LOOSER than the declarations it is replacing,
// accepting spaces, dots and colons that no PVE config id can contain. The
// choke point should carry the rule the callers have been relying on, not a
// weaker one; validateHAResourceID, three functions below, made the same choice
// for the same file.
//
// No length cap, equally deliberately. The route declarations cap at 128, which
// is the right layer for a documented limit, but PVE imposes no maximum of its
// own: a cap here would buy nothing — a string of letters, digits, "_" and "-"
// cannot leave its path segment however long it is — while handing the client a
// way to refuse a name Proxmox minted, which is how the rolling orchestrator
// would lose the ability to re-enable a rule it had disabled.
func validateHAConfigID(kind, id string) error {
	if id == "" {
		return fmt.Errorf("%w: HA %s id is required", ErrInvalidInput, kind)
	}
	if !haConfigIDPattern.MatchString(id) {
		return fmt.Errorf("%w: HA %s id %q must start with a letter and contain only letters, digits, underscore and dash", ErrInvalidInput, kind, id)
	}
	return nil
}

func (c *Client) GetHAResource(ctx context.Context, sid string) (*HAResource, error) {
	if err := validateHAResourceID(sid); err != nil {
		return nil, err
	}
	// Use raw SID (e.g. "vm:100") — Proxmox rejects percent-encoded colons in HA
	// SID paths. validateHAResourceID above is what makes that safe.
	path := "/cluster/ha/resources/" + sid
	var res HAResource
	if err := c.do(ctx, path, &res); err != nil {
		return nil, fmt.Errorf("get HA resource %s: %w", sid, err)
	}
	return &res, nil
}
func (c *Client) UpdateHAResource(ctx context.Context, sid string, params UpdateHAResourceParams) error {
	if err := validateHAResourceID(sid); err != nil {
		return err
	}
	form := url.Values{}
	if params.State != nil {
		form.Set("state", *params.State)
	}
	if params.Group != nil {
		if *params.Group == "" {
			// Taking the resource out of its group has to unset the property;
			// Proxmox refuses group= outright. The update's `group` is
			// get_standard_option('pve-ha-group-id') (pve-ha-manager
			// src/PVE/HA/Tools.pm), `format => 'pve-configid'`, and
			// PVE::JSONSchema's check_prop runs a format on every defined
			// value, the empty string included, where pve_verify_configid dies
			// "invalid configuration ID ''" (pve-common src/PVE/JSONSchema.pm)
			// — a 400 before the update code runs. The value does arrive as ""
			// rather than dropped: decode_urlencoded (pve-http-server
			// src/PVE/APIServer/AnyEvent.pm) splits each pair into a
			// two-variable list, which keeps a trailing empty field.
			//
			// `delete` is SectionConfig's generic unset list, which
			// update_single_resource_config_inplace (src/PVE/HA/Config.pm)
			// applies after storing the other values. group is optional and
			// not fixed there, and deleting a key the resource does not have is
			// a no-op — on PVE 9 as well, whose update refuses only a NON-empty
			// group once groups have been migrated to rules. A second key to
			// unset has to join this one list rather than Set() over it.
			form.Set("delete", "group")
		} else {
			form.Set("group", *params.Group)
		}
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
	if err := validateHAResourceID(sid); err != nil {
		return err
	}
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
	if err := validateHAConfigID("group", group); err != nil {
		return nil, err
	}
	path := "/cluster/ha/groups/" + url.PathEscape(group)
	var g HAGroup
	if err := c.do(ctx, path, &g); err != nil {
		return nil, fmt.Errorf("get HA group %s: %w", group, err)
	}
	return &g, nil
}
func (c *Client) UpdateHAGroup(ctx context.Context, group string, params UpdateHAGroupParams) error {
	if err := validateHAConfigID("group", group); err != nil {
		return err
	}
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
	if err := validateHAConfigID("group", group); err != nil {
		return err
	}
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
