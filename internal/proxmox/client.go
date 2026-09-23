package proxmox

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ClientConfig holds the configuration for creating a new Proxmox API client.
//
// TokenSecret is the cluster's live, long-lived API token secret in plaintext.
// It arrives here straight out of crypto.Decrypt and lives until newAPIClient
// folds it into an Authorization header, and the fourteen non-test
// construction sites are spread across six packages — most of them a few lines
// from a log call naming the cluster or node the config belongs to. String,
// GoString, LogValue and MarshalJSON keep it out of every rendering that
// dispatches on the type, so the first `%+v`, `%#v`, `slog.Any("cfg", cfg)` or
// `json.Marshal` someone writes while debugging a failed connection is already
// closed rather than merely unwritten.
//
// Between them the four cover: %v, %s, %q, %x, %X, %+v, fmt.Sprint and error
// wrapping via String; %#v via GoString (fmt dispatches GoStringer there, NOT
// Stringer — that is the one that is easy to forget); structured logs via
// LogValue; and encoding/json via MarshalJSON, including a ClientConfig
// reached as an EXPORTED field of a larger struct. MarshalJSON is not
// optional even though nothing marshals one today: cmd/nexara/main.go builds
// the production logger with slog.NewJSONHandler (:64 and :272, the only two
// handler constructions outside tests), and for a KindAny value that is not
// itself a LogValuer the JSON handler MARSHALS rather than calling String — so
// a wrapper struct holding a ClientConfig leaks through slog with only the
// print trio in place.
//
// Adding MarshalJSON is safe because nothing serialises this type: it has no
// json tags, is never persisted, and every literal in the tree is passed
// straight to NewClient or NewPBSClient. The single exception is
// internal/rolling/orchestrator.go's failoverTarget.config, which is handed to
// NewClient too.
//
// What is NOT covered, listed so the set above is not mistaken for
// "everything". None is reachable today; they are recorded because the next
// person to create one of these shapes should know it is not covered:
//
//   - A verb fmt cannot dispatch these for — %d, %t, %p, %c, %f — falls back
//     to printing the fields and shows the secret.
//   - A ClientConfig in an UNEXPORTED field. fmt cannot call a method through
//     one (reflect.Value.CanInterface is false), so %+v and %#v of the OUTER
//     struct print the raw fields. This is a live shape:
//     internal/rolling/orchestrator.go's failoverTarget holds one in
//     `config`. Selecting the field — `t.config` — IS covered, because that
//     value can be interfaced; only walking the enclosing struct is not.
//     Do NOT read that as "the classifier would have caught an exported one".
//     The struct-field classifier in
//     internal/api/handlers/proxmox_read_credentials_test.go could not cover
//     this type either way: readStructs() lists three handler-RESPONSE
//     structs and walks each one's own fields, skipping anything that is not
//     a string — so a ClientConfig field is passed over whether it is
//     exported or not, and failoverTarget was never in scope to begin with.
//   - An ANONYMOUS embed. `struct{ ClientConfig; Extra string }` promotes
//     these methods to the outer type, so json.Marshal emits only the
//     redacted object and silently drops Extra, and %v renders only the
//     inner. Safe for the secret, wrong for everything else — embed it as a
//     NAMED field.
//   - Reflection encoders that ignore MarshalJSON: encoding/xml and
//     encoding/gob both emit the secret verbatim.
//
// BaseURL, TokenID and TLSFingerprint stay visible, and a rendering that
// identifies nothing is not worth emitting. All three are pinned as survivors
// on every case of TestGuard_ClientConfigNeverPrintsItsTokenSecret, so a
// redactor that got over-eager and dropped one fails a named test.
//
// Timeout is emitted by all four methods but is deliberately NOT pinned. It is
// not a credential, nothing depends on it surviving, and its representation
// differs per route — "30s" from String, and from LogValue under a text
// handler but the nanosecond integer under the JSON handler production
// actually uses, the nanosecond integer from GoString, a JSON string from
// MarshalJSON — so asserting it would cost
// four survivor-set variants to protect a field nobody is protecting. Drop it
// from a rendering if it ever gets in the way; its presence is a convenience,
// not a guarantee.
//
// TokenID is the principal ("user@realm!tokenname"), not the credential: the
// repo already treats it as non-secret — db.Cluster carries it as
// `json:"token_id"`, handlers.clusterCredential keeps it visible in exactly
// this shape, and the rollback slog.Error in handlers/clusters.go logs it as a
// bare attr, independently of that type's LogValue. A TLS certificate
// fingerprint is public by definition; see the classification in
// internal/api/handlers/proxmox_read_credentials_test.go.
//
// Unlike proxmox.TargetEndpoint there is no PropertyString sibling, because
// nothing needs the whole struct rendered in credential-bearing form:
// newAPIClient reads TokenID and TokenSecret as fields and builds the
// Authorization header itself (buildAuthHeader, api_client.go), so the
// unredacted route never goes through a method fmt could reach by reflex.
//
// All four have VALUE receivers on purpose. fmt and encoding/json skip a
// pointer-receiver method on a value they cannot address, and every call site
// passes a ClientConfig by value, so a pointer receiver here would compile,
// lint clean and redact nothing.
type ClientConfig struct {
	BaseURL        string
	TokenID        string
	TokenSecret    string
	TLSFingerprint string // SHA-256 fingerprint; empty = use system CA pool.
	Timeout        time.Duration
}

// String is the redacted rendering reached by %v, %s, %q, %x, %X, fmt.Sprint
// and error wrapping.
func (c ClientConfig) String() string {
	return "proxmox.ClientConfig{base_url:" + c.BaseURL +
		" token_id:" + c.TokenID +
		" token_secret:REDACTED tls_fingerprint:" + c.TLSFingerprint +
		" timeout:" + c.Timeout.String() + "}"
}

// GoString closes the route String cannot: %#v dispatches GoStringer, and
// without this it prints the struct literal with the secret in it — for the
// value, for a pointer to it, and for anything holding one as an exported
// field.
func (c ClientConfig) GoString() string {
	// Timeout as its nanosecond integer, which is what %#v prints for a
	// time.Duration. GoString is supposed to read like the Go literal that
	// would rebuild the value; "30s" would not compile.
	return `proxmox.ClientConfig{BaseURL:"` + c.BaseURL +
		`", TokenID:"` + c.TokenID +
		`", TokenSecret:"REDACTED", TLSFingerprint:"` + c.TLSFingerprint +
		`", Timeout:` + strconv.FormatInt(int64(c.Timeout), 10) + `}`
}

// LogValue keeps the secret out of structured logs while leaving enough to say
// which endpoint a line is about.
func (c ClientConfig) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("base_url", c.BaseURL),
		slog.String("token_id", c.TokenID),
		slog.String("tls_fingerprint", c.TLSFingerprint),
		slog.Duration("timeout", c.Timeout),
	)
}

// MarshalJSON closes the encoding/json route, which slog.NewJSONHandler takes
// for any ClientConfig it reaches through a wrapper rather than directly.
//
// Only the marshal direction is overridden — UnmarshalJSON is a separate
// interface, so decoding is unaffected. Nothing decodes a ClientConfig today;
// anything that starts to must carry TokenSecret itself rather than expect it
// back out, and that fails closed: newAPIClient rejects an empty TokenSecret
// outright, so a round-tripped config cannot build a client that
// authenticates as nobody.
func (c ClientConfig) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		BaseURL        string `json:"base_url"`
		TokenID        string `json:"token_id"`
		TLSFingerprint string `json:"tls_fingerprint"`
		Timeout        string `json:"timeout"`
	}{c.BaseURL, c.TokenID, c.TLSFingerprint, c.Timeout.String()})
}

// Client communicates with a single Proxmox VE host.
type Client struct {
	*apiClient

	// console shortens the bounds on a console's waits for tests. Nothing
	// else sets it, and its zero value means the defaults — see consoleWaits.
	console consoleTimeouts
}

// NewClient creates a Client from the given config.
func NewClient(cfg ClientConfig) (*Client, error) {
	ac, err := newAPIClient(cfg, "PVEAPIToken")
	if err != nil {
		return nil, err
	}
	return &Client{apiClient: ac}, nil
}

// validateVMID rejects non-positive VM IDs.
func validateVMID(vmid int) error {
	if vmid <= 0 {
		return fmt.Errorf("invalid VMID: %d", vmid)
	}
	return nil
}

// validateNodeName guards a caller-supplied node name before it becomes
// exactly one segment of a Proxmox request path. 144 methods on this client
// open with it — every one that addresses a node.
//
// It DELEGATES rather than carrying a rule of its own, which it did until this
// change, and the difference was a live gap and not a stylistic one. The rule
// it carried refused "", any "/", and ".." as a SUBSTRING — so a bare "." went
// through, and a "." is a path segment that disappears: /nodes/./tasks/{upid}
// resolves onto /nodes/tasks/{upid} the moment pveproxy normalises the path.
// Two callers CHOOSE this value rather than reading it back from Proxmox, so
// the gap was reachable:
//
//   - extractNodeFromUPID (internal/api/handlers/vms.go) takes colon-field 1 of
//     a UPID that taskUPID has already percent-DECODED, so
//     "UPID:.:0:0:0:x::root@pam:" names the node ".". The UPID guard cannot
//     catch that — validateTaskUPID sees a string with no separator in it, and
//     passes — and the two GET task routes are gated on view:task, which the
//     built-in Viewer role holds.
//   - POST /api/v1/tasks (registry_tasks.go) files a caller-supplied "node" on
//     a row marked running, and reconcileRunningTasks
//     (internal/collector/task_reconcile.go) replays it through GetTaskStatus
//     on every sync tick with the server's own credentials and nobody
//     watching. As with a UPID, the traversal need not be issued by the caller
//     who wrote it. That route declared "node" as an optional string with a
//     length cap and NO pattern when this guard was written; it now carries
//     node-name-or-empty. The guard stays regardless — it is the choke point
//     for 144 call sites across eight packages, most of which reach these
//     methods with no route declaration in between at all.
//
// Delegating also fixes the status code. This was the one member of the family
// returning a bare fmt.Errorf instead of wrapping ErrInvalidInput, and
// mapProxmoxError (internal/api/handlers/proxmox_error.go) turns ErrInvalidInput
// into a 400 and everything it does not recognise into a 500 — so an empty node
// name surfaced as a 500 reading like a Proxmox outage rather than as the
// caller's own bad input.
//
// No pattern and no length cap, deliberately. validateHAConfigID records the
// no-cap half at length; do NOT read it for the other half, because it reaches
// the opposite conclusion there and says so — it chose a pattern precisely so
// the client would not be looser than the declarations it replaced. The
// no-pattern reasoning for a node name is the next three sentences and nothing
// else. Proxmox's own rule is pve_verify_node_name in pve-common
// src/PVE/JSONSchema.pm — `^([a-zA-Z0-9]([a-zA-Z0-9\-]*[a-zA-Z0-9])?)\z`:
// letters, digits and dash, alphanumeric at both ends, with no maximum stated.
// Transcribing it HERE would buy nothing a segment guard does not already buy —
// such a name cannot leave its path segment however long it is — while handing
// the client a way to refuse a name Proxmox itself minted. The collector, the
// scheduler and the DRS, rolling and migration engines all reach these methods
// with node names PVE chose and no route declaration in between, and an
// over-tight guard on that path fails SILENTLY: see the note on validateTaskUPID,
// where the same reasoning is worked through for the unattended replay. The
// documented limit belongs at the route declarations, which carry apischema's
// node-name format (internal/api/apischema/catalogue.go); this function only has
// to keep the value inside its own segment.
//
// A "%" is not refused, unlike in validateVolumeID. Every path this package
// builds from a node name writes url.PathEscape(node), which re-encodes a
// percent to %25 so it arrives as the character the caller meant — the same
// distinction validatePathSegmentAllowingSlash records.
//
// The ".."-as-a-SUBSTRING ban is gone ON PURPOSE; do not put it back. ".."
// traverses only as a whole segment, this value is always escaped into one
// segment, and validatePathSegment refuses the whole-segment form outright.
// What the substring form refused instead was "pve..01" — a name the node-name
// format itself accepts, and which failed as a 500 before this change.
func validateNodeName(node string) error {
	return validatePathSegment("node name", node)
}

// validatePathSegment guards a caller-supplied value that becomes exactly one
// segment of a Proxmox request path.
//
// url.PathEscape alone is not enough for this. It escapes "/" but leaves "."
// and ".." untouched, so an escaped "." or ".." still resolves upward once
// pveproxy normalises the path — "." onto the parent collection, which is
// often a different endpoint with different permissions, and ".." one level
// above that. DELETE /nodes/{node}/network/. is the worked example: it
// becomes DELETE /nodes/{node}/network, Proxmox's "revert pending network
// config" — and ".." climbs one level further, onto the node itself.
//
// The control-character refusal is not about the path — url.PathEscape encodes
// those — but about where the value goes AFTERWARDS. Callers put it in a
// TrackTask description and an audit row with %s, and view:audit is granted to
// every Viewer by default, so a newline or an ANSI escape smuggled into a name
// is text other people read. It became reachable when the API layer started
// percent-DECODING these path parameters: the declarations match the value as
// it arrives, and no pattern in the catalogue can see a control byte through a
// "%0A". The sibling below makes the same refusal.
func validatePathSegment(kind, value string) error {
	if value == "" {
		return fmt.Errorf("%w: %s is required", ErrInvalidInput, kind)
	}
	if value == "." || value == ".." {
		return fmt.Errorf("%w: %q is not a valid %s", ErrInvalidInput, value, kind)
	}
	if strings.ContainsAny(value, `/\`) {
		return fmt.Errorf("%w: %s %q must not contain a path separator", ErrInvalidInput, kind, value)
	}
	if hasControlChar(value) {
		return fmt.Errorf("%w: %s %q contains a control character", ErrInvalidInput, kind, value)
	}
	return nil
}

// validatePathSegmentAllowingSlash is validatePathSegment for the one shape it
// cannot express: a value that becomes exactly one segment of a Proxmox request
// path after url.PathEscape, and whose own text legitimately contains a slash.
//
// A firewall IP set entry is the only such value in this client. Its id is a
// CIDR — "192.0.2.0/24" — and the entry lives at
// /cluster/firewall/ipset/{name}/{cidr}, so the slash has to reach Proxmox as
// %2F rather than be refused; that is exactly what PVE's own UI sends, and
// url.PathEscape produces it. The blanket separator ban validatePathSegment
// makes would turn every CIDR entry into a 400.
//
// The slash still cannot be waved through unchecked, because Proxmox decodes
// the escape before it resolves the path. The capture-server run that proved
// it is recorded on forbiddenVolumeIDChars (client_storage.go): a volume id is
// interpolated RAW, so "%2e%2e%2f" reached Proxmox byte-for-byte and decoded
// to "../" there. That measures the far side, which is the half that matters —
// an escape this client produces arrives at the same decoder. So the guard is
// the per-component one
// validateVolumeID makes — every "/"-delimited piece has to be a real name —
// which refuses "../.." while taking "192.0.2.0/24".
//
// A "%" needs no exclusion here, unlike in validateVolumeID: that value is
// interpolated raw, this one goes through url.PathEscape, which re-encodes the
// percent so it arrives as the literal character the caller meant.
func validatePathSegmentAllowingSlash(kind, value string) error {
	if value == "" {
		return fmt.Errorf("%w: %s is required", ErrInvalidInput, kind)
	}
	if strings.Contains(value, `\`) {
		return fmt.Errorf("%w: %s %q must not contain a backslash", ErrInvalidInput, kind, value)
	}
	if hasControlChar(value) {
		return fmt.Errorf("%w: %s %q contains a control character", ErrInvalidInput, kind, value)
	}
	components := strings.Split(value, "/")
	// ONE slash, which is what "AllowingSlash" means: a CIDR is "addr/len" and
	// an entry that is neither a CIDR nor an address is an alias name with no
	// slash at all. Refusing the rest is about DESCENT rather than ascent — the
	// per-component check below stops "../..", but "a/b/c/d" passes it and,
	// once Proxmox decodes the escapes, lands the request three levels deeper
	// than the one segment this value is positioned in.
	if len(components) > 2 {
		return fmt.Errorf("%w: %s %q has more than one path separator", ErrInvalidInput, kind, value)
	}
	for _, component := range components {
		if component == "" || component == "." || component == ".." {
			return fmt.Errorf("%w: %s %q has a bad path segment %q", ErrInvalidInput, kind, value, component)
		}
	}
	return nil
}

// stringVal safely extracts a string from a map entry.
func stringVal(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// sdnZoneCreateToForm converts SDN zone create params to url.Values.
func sdnZoneCreateToForm(p CreateSDNZoneParams) url.Values {
	form := url.Values{}
	form.Set("zone", p.Zone)
	form.Set("type", p.Type)
	if p.Bridge != "" {
		form.Set("bridge", p.Bridge)
	}
	if p.Peers != "" {
		form.Set("peers", p.Peers)
	}
	if p.Nodes != "" {
		form.Set("nodes", p.Nodes)
	}
	if p.IPAM != "" {
		form.Set("ipam", p.IPAM)
	}
	if p.DNS != "" {
		form.Set("dns", p.DNS)
	}
	if p.ReverseDNS != "" {
		form.Set("reversedns", p.ReverseDNS)
	}
	if p.DNSZone != "" {
		form.Set("dnszone", p.DNSZone)
	}
	if p.VLANProtocol != "" {
		form.Set("vlan-protocol", p.VLANProtocol)
	}
	if p.Controller != "" {
		form.Set("controller", p.Controller)
	}
	if p.ExitNodes != "" {
		form.Set("exitnodes", p.ExitNodes)
	}
	if p.Mac != "" {
		form.Set("mac", p.Mac)
	}
	if p.Tag != 0 {
		form.Set("tag", strconv.Itoa(p.Tag))
	}
	if p.MTU != 0 {
		form.Set("mtu", strconv.Itoa(p.MTU))
	}
	if p.VRFVxlan != 0 {
		form.Set("vrf-vxlan", strconv.Itoa(p.VRFVxlan))
	}
	if p.AdvSubnets != 0 {
		form.Set("advertise-subnets", strconv.Itoa(p.AdvSubnets))
	}
	if p.DisableArp != 0 {
		form.Set("disable-arp-nd-suppression", strconv.Itoa(p.DisableArp))
	}
	return form
}

// sdnZoneUpdateToForm converts SDN zone update params to url.Values.
func sdnZoneUpdateToForm(p UpdateSDNZoneParams) url.Values {
	form := url.Values{}
	if p.Bridge != "" {
		form.Set("bridge", p.Bridge)
	}
	if p.Peers != "" {
		form.Set("peers", p.Peers)
	}
	if p.Nodes != "" {
		form.Set("nodes", p.Nodes)
	}
	if p.IPAM != "" {
		form.Set("ipam", p.IPAM)
	}
	if p.DNS != "" {
		form.Set("dns", p.DNS)
	}
	if p.ReverseDNS != "" {
		form.Set("reversedns", p.ReverseDNS)
	}
	if p.DNSZone != "" {
		form.Set("dnszone", p.DNSZone)
	}
	if p.VLANProtocol != "" {
		form.Set("vlan-protocol", p.VLANProtocol)
	}
	if p.Controller != "" {
		form.Set("controller", p.Controller)
	}
	if p.ExitNodes != "" {
		form.Set("exitnodes", p.ExitNodes)
	}
	if p.Mac != "" {
		form.Set("mac", p.Mac)
	}
	if p.Tag != 0 {
		form.Set("tag", strconv.Itoa(p.Tag))
	}
	if p.MTU != 0 {
		form.Set("mtu", strconv.Itoa(p.MTU))
	}
	if p.VRFVxlan != 0 {
		form.Set("vrf-vxlan", strconv.Itoa(p.VRFVxlan))
	}
	if p.AdvSubnets != 0 {
		form.Set("advertise-subnets", strconv.Itoa(p.AdvSubnets))
	}
	if p.DisableArp != 0 {
		form.Set("disable-arp-nd-suppression", strconv.Itoa(p.DisableArp))
	}
	return form
}

// sdnVNetCreateToForm converts SDN VNet create params to url.Values.
func sdnVNetCreateToForm(p CreateSDNVNetParams) url.Values {
	form := url.Values{}
	form.Set("vnet", p.VNet)
	form.Set("zone", p.Zone)
	if p.Alias != "" {
		form.Set("alias", p.Alias)
	}
	if p.Tag != 0 {
		form.Set("tag", strconv.Itoa(p.Tag))
	}
	if p.VLANAware != 0 {
		form.Set("vlanaware", strconv.Itoa(p.VLANAware))
	}
	if p.Isolate != 0 {
		form.Set("isolate", strconv.Itoa(p.Isolate))
	}
	return form
}

// sdnVNetUpdateToForm converts SDN VNet update params to url.Values.
func sdnVNetUpdateToForm(p UpdateSDNVNetParams) url.Values {
	form := url.Values{}
	if p.Zone != "" {
		form.Set("zone", p.Zone)
	}
	if p.Alias != "" {
		form.Set("alias", p.Alias)
	}
	if p.Tag != 0 {
		form.Set("tag", strconv.Itoa(p.Tag))
	}
	if p.VLANAware != 0 {
		form.Set("vlanaware", strconv.Itoa(p.VLANAware))
	}
	if p.Isolate != 0 {
		form.Set("isolate", strconv.Itoa(p.Isolate))
	}
	return form
}

// sdnSubnetCreateToForm converts SDN subnet create params to url.Values.
func sdnSubnetCreateToForm(p CreateSDNSubnetParams) url.Values {
	form := url.Values{}
	form.Set("subnet", p.Subnet)
	if p.Gateway != "" {
		form.Set("gateway", p.Gateway)
	}
	if p.Type != "" {
		form.Set("type", p.Type)
	}
	if p.SNAT != 0 {
		form.Set("snat", strconv.Itoa(p.SNAT))
	}
	if p.DHCPRange != "" {
		form.Set("dhcp-range", p.DHCPRange)
	}
	if p.DHCPDNSServer != "" {
		form.Set("dhcp-dns-server", p.DHCPDNSServer)
	}
	return form
}

// sdnSubnetUpdateToForm converts SDN subnet update params to url.Values.
func sdnSubnetUpdateToForm(p UpdateSDNSubnetParams) url.Values {
	form := url.Values{}
	if p.Gateway != "" {
		form.Set("gateway", p.Gateway)
	}
	if p.SNAT != 0 {
		form.Set("snat", strconv.Itoa(p.SNAT))
	}
	if p.DHCPRange != "" {
		form.Set("dhcp-range", p.DHCPRange)
	}
	if p.DHCPDNSServer != "" {
		form.Set("dhcp-dns-server", p.DHCPDNSServer)
	}
	return form
}

// firewallRuleToForm converts a FirewallRuleParams to url.Values for the Proxmox API.
func firewallRuleToForm(rule FirewallRuleParams) url.Values {
	form := url.Values{}
	if rule.Type != "" {
		form.Set("type", rule.Type)
	}
	if rule.Action != "" {
		form.Set("action", rule.Action)
	}
	if rule.Source != "" {
		form.Set("source", rule.Source)
	}
	if rule.Dest != "" {
		form.Set("dest", rule.Dest)
	}
	if rule.Sport != "" {
		form.Set("sport", rule.Sport)
	}
	if rule.Dport != "" {
		form.Set("dport", rule.Dport)
	}
	if rule.Proto != "" {
		form.Set("proto", rule.Proto)
	}
	form.Set("enable", strconv.Itoa(rule.Enable))
	if rule.Comment != "" {
		form.Set("comment", rule.Comment)
	}
	if rule.Macro != "" {
		form.Set("macro", rule.Macro)
	}
	if rule.Log != "" {
		form.Set("log", rule.Log)
	}
	if rule.Iface != "" {
		form.Set("iface", rule.Iface)
	}
	return form
}

// firewallOptionsToForm converts FirewallOptions to url.Values.
func firewallOptionsToForm(opts FirewallOptions) url.Values {
	form := url.Values{}
	if opts.Enable != nil {
		form.Set("enable", strconv.Itoa(*opts.Enable))
	}
	if opts.PolicyIn != "" {
		form.Set("policy_in", opts.PolicyIn)
	}
	if opts.PolicyOut != "" {
		form.Set("policy_out", opts.PolicyOut)
	}
	if opts.LogLevelIn != "" {
		form.Set("log_level_in", opts.LogLevelIn)
	}
	if opts.LogLevelOut != "" {
		form.Set("log_level_out", opts.LogLevelOut)
	}
	return form
}

// isAgentNotRunning checks if the error indicates the QEMU guest agent is not running.
func isAgentNotRunning(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == 500 {
		return strings.Contains(apiErr.Message, "QEMU guest agent is not running") ||
			strings.Contains(apiErr.Message, "guest agent") ||
			strings.Contains(apiErr.Message, "not running")
	}
	return false
}
