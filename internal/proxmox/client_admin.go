package proxmox

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

func (c *Client) GetCPUModels(ctx context.Context, node string) ([]CPUModel, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var models []CPUModel
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/capabilities/qemu/cpu", &models); err != nil {
		return nil, fmt.Errorf("get CPU models on %s: %w", node, err)
	}
	return models, nil
}

// GetCPUFlags returns the VM-specific CPU flags Proxmox understands, together
// with the nodes each one is usable on. Requires PVE with the
// capabilities/qemu/cpu-flags endpoint; older versions answer 501.
func (c *Client) GetCPUFlags(ctx context.Context, node string) ([]CPUFlag, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var flags []CPUFlag
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/capabilities/qemu/cpu-flags", &flags); err != nil {
		return nil, fmt.Errorf("get CPU flags on %s: %w", node, err)
	}
	return flags, nil
}

func (c *Client) GetResourcePools(ctx context.Context) ([]ResourcePool, error) {
	var pools []ResourcePool
	if err := c.do(ctx, "/pools", &pools); err != nil {
		return nil, fmt.Errorf("get resource pools: %w", err)
	}
	return pools, nil
}
func (c *Client) CreateResourcePool(ctx context.Context, params CreatePoolParams) error {
	form := url.Values{}
	form.Set("poolid", params.PoolID)
	if params.Comment != "" {
		form.Set("comment", params.Comment)
	}
	if err := c.doPost(ctx, "/pools", form, nil); err != nil {
		return fmt.Errorf("create resource pool %s: %w", params.PoolID, err)
	}
	return nil
}

// --- Addressing an existing resource pool ---
//
// GetResourcePool, UpdateResourcePool and DeleteResourcePool each interpolate
// a caller-supplied pool id into the request PATH, so all three take
// validatePathSegment: the same guard validateNodeName and validateTaskUPID
// delegate to, with the same deliberate looseness — no charset, no length cap.
//
// They must NOT take a pool-NAME rule. These three address something PVE
// already minted, and verify_poolname (pve-access-control) is wide: a leading
// dot or dash is a legal pool name, and a pool created at the PVE console can
// carry one. Refusing it here is how an object becomes undeletable, which is
// the trap poolIDParam (internal/api/registry_pools.go) already documents from
// the declaration side.
//
// What validatePathSegment adds over that width is the pair of values whose
// meaning depends on who reads the path first. url.PathEscape encodes "/" but
// leaves "." and ".." alone entirely, so those two travel raw. pveproxy takes
// them literally (see validatePathSegment), as a pool NAMED "." or "..", which
// verify_poolname admits. A normalising proxy in front of pveproxy resolves
// them upward instead:
//
//	poolID="."   GET /pools     the pool COLLECTION, whose body is a list
//	                            where a detail object is expected
//	poolID=".."  DELETE /       one level further up: the API root, which
//	                            has no delete form at all
//
// Neither is believed to reach a destructive PVE handler even there: PVE's own
// collection-level delete takes the pool id as a PARAMETER, and this client
// sends none, so a DELETE landing on /pools (poolID=".") is a parameter error
// rather than a mass delete, and the API root has no delete to reach. That is
// a reading of the PVE API and not something measured against a live cluster
// from here, which is exactly why it is not the argument for closing the gap
// — the argument is that a request resolving onto an endpoint the caller did
// not name is not a thing to reason about case by case. It is closed here
// rather than at the route, for the reason recorded on the snapshot address
// methods (client_guests.go, "Addressing an existing snapshot") and on
// validateTaskUPID: a check in the CALLER is one the next caller inherits
// nothing from. When this guard was added the ONLY thing
// refusing anything on these three was the pool id's rule in
// internal/api/registry_pools.go — which stopped a slash, and matched "." and
// ".." exactly as it matched any other name.
//
// The price is a pool literally named "." or "..": Nexara cannot address it,
// and cannot move a guest into it or out of it, since SetVMPool
// (internal/api/handlers/vms.go) goes through UpdateResourcePool for both
// halves of a move. It does not CREATE one: CreateResourcePool (above) would
// send either name, since it carries the id as a form field, but POST /pools
// refuses both at its declaration (pve-poolid-new, poolCreateIDParam in
// internal/api/registry_pools.go). One made outside Nexara can still exist,
// though, and Nexara can still create a guest INTO it, because CreateVM and
// CreateCT send `pool` as a form field too — and a guest created there can
// never be moved out through Nexara. Switching these three
// methods to the forms PVE now prefers — PUT, DELETE and GET /pools with
// poolid as a parameter; the {poolid} path forms are deprecated upstream —
// takes the id out of this client's path, but fixes only that leg: Nexara's
// own pool routes carry the id in their path too, and a browser resolves a
// "." or ".." segment before the request leaves, so lifting the dot limit
// also needs those routes to carry the id outside the path. Switching is a
// decision not taken here. Group and role ids have no such form, so
// validateAccessName's price stands.
//
// validateAccessName (client_access.go) is the precedent and the proof that
// this is the house rule rather than a preference: PVE group and role ids are
// the same charset in the same kind of path slot, and that function refuses
// ".", ".." by name for the same normalising-proxy reason. Pool ids were the
// one family carrying that charset with no client-side guard at all.
//
// The declaration side has since caught up: both families now read one
// catalogue entry, apischema's path-safe-dotted-name, which carves the same
// pair out at the route. That does NOT make this check redundant, and it is
// not the layer to delete if the two ever look duplicated — this one is the
// choke point every caller inherits, and the rule over there reaches only the
// HTTP callers. The two are separately killable by design: the tests below
// fail on a change here and stay green on a change there.
//
// CreateResourcePool is deliberately not in this list. Its pool id is a form
// field — form.Set("poolid", …) — not a path segment, so nesting is legal
// there and no traversal is possible. That asymmetry is the same one
// poolCreateIDParam records: a pool PVE will happily create under a name this
// URL shape cannot address.

func (c *Client) GetResourcePool(ctx context.Context, poolID string) (*ResourcePoolDetail, error) {
	if err := validatePathSegment("pool id", poolID); err != nil {
		return nil, err
	}
	path := "/pools/" + url.PathEscape(poolID)
	var pool ResourcePoolDetail
	if err := c.do(ctx, path, &pool); err != nil {
		return nil, fmt.Errorf("get resource pool %s: %w", poolID, err)
	}
	return &pool, nil
}
func (c *Client) UpdateResourcePool(ctx context.Context, poolID string, params UpdatePoolParams) error {
	if err := validatePathSegment("pool id", poolID); err != nil {
		return err
	}
	form := url.Values{}
	if params.Comment != nil {
		form.Set("comment", *params.Comment)
	}
	if params.VMs != "" {
		form.Set("vms", params.VMs)
	}
	if params.Storage != "" {
		form.Set("storage", params.Storage)
	}
	if params.Delete != "" {
		form.Set("delete", params.Delete)
	}
	path := "/pools/" + url.PathEscape(poolID)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update resource pool %s: %w", poolID, err)
	}
	return nil
}
func (c *Client) DeleteResourcePool(ctx context.Context, poolID string) error {
	if err := validatePathSegment("pool id", poolID); err != nil {
		return err
	}
	path := "/pools/" + url.PathEscape(poolID)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete resource pool %s: %w", poolID, err)
	}
	return nil
}
func (c *Client) GetMetricServers(ctx context.Context) ([]MetricServerConfig, error) {
	var servers []MetricServerConfig
	if err := c.do(ctx, "/cluster/metrics/server", &servers); err != nil {
		return nil, fmt.Errorf("get metric servers: %w", err)
	}
	return servers, nil
}
func (c *Client) CreateMetricServer(ctx context.Context, params CreateMetricServerParams) error {
	form := url.Values{}
	form.Set("type", params.Type)
	form.Set("server", params.Server)
	form.Set("port", strconv.Itoa(params.Port))
	if params.Disable != nil {
		form.Set("disable", strconv.Itoa(*params.Disable))
	}
	if params.MTU > 0 {
		form.Set("mtu", strconv.Itoa(params.MTU))
	}
	if params.Timeout > 0 {
		form.Set("timeout", strconv.Itoa(params.Timeout))
	}
	if params.Proto != "" {
		form.Set("proto", params.Proto)
	}
	if params.Path != "" {
		form.Set("path", params.Path)
	}
	if params.InfluxDBProto != "" {
		form.Set("influxdbproto", params.InfluxDBProto)
	}
	if params.Organization != "" {
		form.Set("organization", params.Organization)
	}
	if params.Bucket != "" {
		form.Set("bucket", params.Bucket)
	}
	if params.Token != "" {
		form.Set("token", params.Token)
	}
	if params.MaxBodySize > 0 {
		form.Set("max-body-size", strconv.Itoa(params.MaxBodySize))
	}
	if params.VerifyCert != nil {
		form.Set("verify-certificate", strconv.Itoa(*params.VerifyCert))
	}
	path := "/cluster/metrics/server/" + url.PathEscape(params.ID)
	if err := c.doPost(ctx, path, form, nil); err != nil {
		return fmt.Errorf("create metric server %s: %w", params.ID, err)
	}
	return nil
}
func (c *Client) GetMetricServer(ctx context.Context, id string) (*MetricServerConfig, error) {
	path := "/cluster/metrics/server/" + url.PathEscape(id)
	var server MetricServerConfig
	if err := c.do(ctx, path, &server); err != nil {
		return nil, fmt.Errorf("get metric server %s: %w", id, err)
	}
	return &server, nil
}
func (c *Client) UpdateMetricServer(ctx context.Context, id string, params UpdateMetricServerParams) error {
	form := url.Values{}
	if params.Server != "" {
		form.Set("server", params.Server)
	}
	if params.Port != nil {
		form.Set("port", strconv.Itoa(*params.Port))
	}
	if params.Disable != nil {
		form.Set("disable", strconv.Itoa(*params.Disable))
	}
	if params.MTU > 0 {
		form.Set("mtu", strconv.Itoa(params.MTU))
	}
	if params.Timeout > 0 {
		form.Set("timeout", strconv.Itoa(params.Timeout))
	}
	if params.Proto != "" {
		form.Set("proto", params.Proto)
	}
	if params.Path != "" {
		form.Set("path", params.Path)
	}
	if params.InfluxDBProto != "" {
		form.Set("influxdbproto", params.InfluxDBProto)
	}
	if params.Organization != "" {
		form.Set("organization", params.Organization)
	}
	if params.Bucket != "" {
		form.Set("bucket", params.Bucket)
	}
	if params.Token != "" {
		form.Set("token", params.Token)
	}
	if params.MaxBodySize > 0 {
		form.Set("max-body-size", strconv.Itoa(params.MaxBodySize))
	}
	if params.VerifyCert != nil {
		form.Set("verify-certificate", strconv.Itoa(*params.VerifyCert))
	}
	if params.Delete != "" {
		form.Set("delete", params.Delete)
	}
	path := "/cluster/metrics/server/" + url.PathEscape(id)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update metric server %s: %w", id, err)
	}
	return nil
}
func (c *Client) DeleteMetricServer(ctx context.Context, id string) error {
	path := "/cluster/metrics/server/" + url.PathEscape(id)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete metric server %s: %w", id, err)
	}
	return nil
}
