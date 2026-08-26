package veeam

import "sort"

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
	PlatformType string  `json:"platformType"`
	Name         string  `json:"name"`
	DisplayName  string  `json:"displayName"`
	HostName     string  `json:"hostName"`
	Type         string  `json:"type"`
	InstanceID   string  `json:"instanceId"`
	UsedInstances float64 `json:"usedInstancesNumber"`
}

// ProxmoxCluster is one Proxmox connection as the license reports it.
type ProxmoxCluster struct {
	// Name is the Veeam-side hostName, e.g. "CRJLAB". Not a platformId —
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
