package veeam

import (
	"encoding/json"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ProxmoxPlatformName is the value VBR 13.1 uses for Proxmox VE across every
// platformName / platformType field.
//
// Proxmox is a first-class EPlatformType on 13.1 — the pre-13.1 guidance that
// it surfaces as "CustomPlatform" is wrong for supported builds
// (platformNameFilter=CustomPlatform returns zero rows), which is one reason
// the minimum version exists.
const ProxmoxPlatformName = "Proxmox"

// tokenResponse is the OAuth2 password/refresh grant body.
//
// Veeam also returns ".issued" and ".expires" as absolute timestamps. They are
// deliberately ignored: they are the VBR host's clock, and trusting a remote
// clock to decide when our cached token expires makes refresh timing depend on
// clock skew. ExpiresIn is a duration, so it doesn't.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

// String, GoString, LogValue and MarshalJSON keep both live tokens out of
// fmt, slog and encoding/json. This struct is decoded in requestToken()
// (internal/veeam/auth.go) with two error returns immediately below it — the
// decode failure and the "carried no access_token" check — and `%v` of the
// decoded response is the obvious thing to add to either when a VBR login is
// failing for reasons the error text does not explain.
//
// Both json tags stay: the struct exists only to be UNMARSHALLED from VBR's
// OAuth2 grant body, so json:"-" would break authentication outright. Only
// the marshal direction is overridden — UnmarshalJSON is a separate
// interface, so decoding is untouched.
//
// MarshalJSON is here even though nothing in the tree marshals a
// tokenResponse, and the reason is worth stating because an earlier version
// of this type left it out on exactly that argument. Production builds its
// logger with slog.NewJSONHandler (cmd/nexara/main.go:64 and :272), and for a
// value that is not itself a LogValuer the JSON handler MARSHALS it where the
// text handler formats it with %+v. A tokenResponse reached as an exported
// field of some context struct — `slog.Error("veeam auth failed", "ctx",
// struct{ Req tokenResponse; URL string }{tr, url})` — therefore goes through
// encoding/json, not through String, and without this method it writes a live
// VBR access and refresh token to stdout in cleartext. slog.Any on the type
// ITSELF is safe either way, because LogValue resolves first; it is the
// wrapper shape that needs this. So a slog call IS a serialisation here, and
// "the realistic mistake is a %v, not a serialisation" was simply wrong.
//
// TokenType and ExpiresIn stay visible. They are not secrets, and they are
// what makes the redacted rendering worth emitting at all — see
// TestGuard_VeeamTokenResponseNeverPrintsItsTokens, which asserts they survive
// so that a String() returning "" cannot pass an absence-only check.
//
// Value receivers: fmt and slog skip a pointer-receiver method on a value they
// cannot address, and tr is held by value in requestToken().
//
// One route stays open and is recorded rather than closed: a tokenResponse
// held in an UNEXPORTED struct field. fmt cannot call a method through one, so
// %+v and %#v of the outer struct print the raw fields. Nothing holds one that
// way today.
func (t tokenResponse) String() string {
	return "tokenResponse{access_token:REDACTED token_type:" + t.TokenType +
		" refresh_token:REDACTED expires_in:" + strconv.Itoa(t.ExpiresIn) + "}"
}

func (t tokenResponse) GoString() string {
	return `veeam.tokenResponse{AccessToken:"REDACTED", TokenType:"` + t.TokenType +
		`", RefreshToken:"REDACTED", ExpiresIn:` + strconv.Itoa(t.ExpiresIn) + `}`
}

func (t tokenResponse) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("token_type", t.TokenType),
		slog.Int("expires_in", t.ExpiresIn),
	)
}

func (t tokenResponse) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		TokenType string `json:"token_type"`
		ExpiresIn int    `json:"expires_in"`
	}{t.TokenType, t.ExpiresIn})
}

// apiErrorBody is VBR's error envelope, identical across every endpoint.
type apiErrorBody struct {
	ErrorCode string `json:"errorCode"`
	Message   string `json:"message"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
}

// ServerInfo is GET /api/v1/serverInfo.
type ServerInfo struct {
	Platform string `json:"platform"`
	VBRID    string `json:"vbrId"`
	Name     string `json:"name"`
	// BuildVersion is the four-part product version, e.g. "13.1.0.411".
	// The 13.1 minimum is enforced against this.
	BuildVersion   string `json:"buildVersion"`
	DatabaseVendor string `json:"databaseVendor"`
}

// License is GET /api/v1/license, trimmed to the fields Nexara acts on.
type License struct {
	Status     string `json:"status"`
	Type       string `json:"type"`
	Edition    string `json:"edition"`
	LicensedTo string `json:"licensedTo"`
	Summary    struct {
		Package                 string            `json:"package"`
		LicensedInstancesNumber float64           `json:"licensedInstancesNumber"`
		UsedInstancesNumber     float64           `json:"usedInstancesNumber"`
		Workload                []LicenseWorkload `json:"workload"`
		// ExpirationDate stays a string rather than a time.Time: it is only
		// ever passed through for display, and an unexpected empty value
		// would fail the whole decode if it were typed.
		ExpirationDate string `json:"expirationDate"`
	} `json:"instanceLicenseSummary"`
}

// LicenseWorkload is one licensed instance. For Proxmox entries HostName is
// the Proxmox cluster name as Veeam knows it — a human-readable correlation
// signal, and the only place a cluster label appears anywhere in the API.
type LicenseWorkload struct {
	PlatformType  string  `json:"platformType"`
	Name          string  `json:"name"`
	DisplayName   string  `json:"displayName"`
	HostName      string  `json:"hostName"`
	Type          string  `json:"type"`
	InstanceID    string  `json:"instanceId"`
	UsedInstances float64 `json:"usedInstancesNumber"`
}

// ProxmoxCluster is one Proxmox connection as the license reports it.
type ProxmoxCluster struct {
	// Name is the Veeam-side hostName, e.g. "cluster01". Not a platformId —
	// that only appears on sessions, restore points and backup objects, so
	// Phase 1 cannot resolve one yet.
	Name string `json:"name"`
	// VMCount is how many licensed instances report this hostName.
	VMCount int `json:"vm_count"`
}

// ProxmoxClusters summarises the Proxmox workloads this license covers,
// ordered by descending VM count then name so the output is stable.
//
// This is what tells an operator at add-time whether the server they just
// connected actually protects any Proxmox guests — a VBR with a valid licence,
// a supported version and zero Proxmox workloads is a successful connection
// that will never produce a single row, and saying so up front beats an empty
// dashboard later.
func (l *License) ProxmoxClusters() []ProxmoxCluster {
	if l == nil {
		return nil
	}
	counts := make(map[string]int)
	var order []string
	for _, w := range l.Summary.Workload {
		if w.PlatformType != ProxmoxPlatformName {
			continue
		}
		if _, seen := counts[w.HostName]; !seen {
			order = append(order, w.HostName)
		}
		counts[w.HostName]++
	}

	out := make([]ProxmoxCluster, 0, len(order))
	for _, name := range order {
		out = append(out, ProxmoxCluster{Name: name, VMCount: counts[name]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].VMCount != out[j].VMCount {
			return out[i].VMCount > out[j].VMCount
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Timestamp is a time the Veeam API emitted.
//
// It exists because the API is not consistent about offsets: session and job
// timestamps carry one ("2026-08-24T22:00:29.381149+05:30") while others do
// not ("2026-02-14T22:37:33" on a backup's creationTime). A plain time.Time
// field fails the whole decode on the second form, which would take down a
// sync for one bad field on one row.
//
// An unparseable or empty value decodes to the zero time rather than an error,
// for the same reason: losing one timestamp is recoverable, losing the listing
// that contained it is not.
type Timestamp struct {
	time.Time
}

// timestampLayouts are tried in order. The offset-bearing forms come first
// because they are what the endpoints Nexara actually polls emit.
var timestampLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05.999999999",
	"2006-01-02T15:04:05",
	"2006-01-02",
}

// UnmarshalJSON decodes any of the layouts above, plus JSON null.
func (t *Timestamp) UnmarshalJSON(data []byte) error {
	s := strings.Trim(string(data), `"`)
	if s == "" || s == "null" {
		t.Time = time.Time{}
		return nil
	}
	for _, layout := range timestampLayouts {
		// An offset-less value is read as UTC. It is the only defensible
		// choice: the alternative is the collector's local zone, which would
		// make the same payload decode differently depending on where Nexara
		// runs.
		if parsed, err := time.Parse(layout, s); err == nil {
			t.Time = parsed
			return nil
		}
	}
	t.Time = time.Time{}
	return nil
}

// MarshalJSON emits RFC3339, or null for the zero time.
func (t Timestamp) MarshalJSON() ([]byte, error) {
	if t.IsZero() {
		return []byte("null"), nil
	}
	return []byte(`"` + t.Format(time.RFC3339) + `"`), nil
}

// Or returns the timestamp's value, or the zero time when the pointer is nil.
// Saves every caller a nil check on the optional fields.
func (t *Timestamp) Or() time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.Time
}
