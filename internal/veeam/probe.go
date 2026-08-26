package veeam

import (
	"context"
	"fmt"
)

// RequiredEdition is the Veeam licence edition that covers Proxmox VE
// workloads. Anything else produces a warning rather than a refusal — Veeam
// itself is the authority that will reject the backup, and a lab on a trial
// that is about to be upgraded is a real case.
const RequiredEdition = "EnterprisePlus"

// ProbeResult is everything a connection test learns about a server. It
// carries no credential and nothing derived from one, so it is safe to return
// from the API and to write into an audit row.
type ProbeResult struct {
	APIRevision       string           `json:"api_revision"`
	ServerName        string           `json:"server_name"`
	BuildVersion      string           `json:"build_version"`
	Platform          string           `json:"platform"`
	LicenseEdition    string           `json:"license_edition"`
	LicenseStatus     string           `json:"license_status"`
	LicenseType       string           `json:"license_type"`
	LicensedTo        string           `json:"licensed_to"`
	LicenseExpiration string           `json:"license_expiration"`
	ProxmoxClusters   []ProxmoxCluster `json:"proxmox_clusters"`
	// Warnings are conditions the operator should see but that do not stop
	// the server being registered.
	Warnings []string `json:"warnings"`
}

// Probe negotiates an API revision, authenticates, and reads the server's
// version and licence.
//
// Two of the four checks are fatal and two are advisory, which is the whole
// point of doing them here rather than letting the first sync discover them:
//
//	auth failure        fatal — nothing else can be attempted
//	version < 13.1      fatal — 13.0.x reports Proxmox jobs as type "Unknown"
//	                    with no lastRun, so every downstream number would be
//	                    silently wrong rather than absent
//	wrong edition       warning — Veeam enforces its own licence
//	no Proxmox workload warning — a perfectly healthy server that will never
//	                    produce a row, which is worth saying now rather than
//	                    leaving as an empty dashboard
//
// The licence read is best-effort: a server that authenticates and meets the
// version floor is usable even if /license is unavailable, so that failure
// degrades to a warning too.
func (c *Client) Probe(ctx context.Context) (*ProbeResult, error) {
	rev := c.Revision()
	if rev == "" {
		negotiated, err := c.NegotiateRevision(ctx)
		if err != nil {
			return nil, err
		}
		rev = negotiated
	}

	info, err := c.ServerInfo(ctx)
	if err != nil {
		return nil, err
	}

	if !buildVersionSupported(info.BuildVersion) {
		return nil, &VersionError{Got: info.BuildVersion, Minimum: MinBuildVersion}
	}

	res := &ProbeResult{
		APIRevision:  rev,
		ServerName:   info.Name,
		BuildVersion: info.BuildVersion,
		Platform:     info.Platform,
		Warnings:     []string{},
	}

	lic, err := c.License(ctx)
	if err != nil {
		res.Warnings = append(res.Warnings,
			fmt.Sprintf("Could not read the licence: %s. Nexara cannot confirm this server is licensed for Proxmox workloads.", err))
		return res, nil
	}

	res.LicenseEdition = lic.Edition
	res.LicenseStatus = lic.Status
	res.LicenseType = lic.Type
	res.LicensedTo = lic.LicensedTo
	res.LicenseExpiration = lic.Summary.ExpirationDate
	res.ProxmoxClusters = lic.ProxmoxClusters()

	if lic.Edition != RequiredEdition {
		res.Warnings = append(res.Warnings,
			fmt.Sprintf("Licence edition is %q; Proxmox VE backup requires %s. Veeam will refuse Proxmox jobs on this edition.",
				lic.Edition, RequiredEdition))
	}
	if lic.Status != "" && lic.Status != "Valid" {
		res.Warnings = append(res.Warnings,
			fmt.Sprintf("Licence status is %q, not \"Valid\".", lic.Status))
	}
	if len(res.ProxmoxClusters) == 0 {
		res.Warnings = append(res.Warnings,
			"This server reports no licensed Proxmox workloads. The connection works, but Nexara will have no Veeam data to show until a Proxmox cluster is added to Veeam.")
	}

	return res, nil
}
