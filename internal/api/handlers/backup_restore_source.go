package handlers

import (
	"net/url"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/gofiber/fiber/v3"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// restoreStorage names the Proxmox storage a restore reads its archive through:
// the one pbs storage of the cluster that points at the PBS server and datastore
// the snapshot lives on and that the guest's node can use.
//
// A restore addresses the snapshot as "<storage>:backup/<type>/<id>/<time>", so
// the storage decides which PBS it is read from. It used to be the first pbs
// storage of the cluster, whatever server and datastore were asked for, which
// with datastores synced between servers restored from a PBS nobody chose.
//
// A storage is a candidate when it is enabled, is available on the node, has no
// namespace — Nexara lists a datastore's root namespace only, so a namespaced
// storage cannot hold the snapshot — and names the datastore (when the request
// does). It belongs to the server when it shares the server's host or its TLS
// fingerprint. No candidate, or more than one, is refused with what to change:
// a restore must not choose between two readings of the request.
func restoreStorage(cfgs []proxmox.StorageConfig, server db.PbsServer, datastore, node string) (string, error) {
	var pbs, matches []string
	for _, cfg := range cfgs {
		if cfg.Type != "pbs" {
			continue
		}
		pbs = append(pbs, cfg.Storage)
		if restoreStorageMatches(cfg, server, datastore, node) {
			matches = append(matches, cfg.Storage)
		}
	}
	sort.Strings(pbs)
	sort.Strings(matches)

	what := "PBS server " + quoted(server.Name)
	if datastore != "" {
		what = "datastore " + quoted(datastore) + " of " + what
	}
	switch {
	case len(pbs) == 0:
		return "", fiber.NewError(fiber.StatusBadRequest, "No PBS storage configured on this PVE cluster. Add the PBS server as storage in PVE first (Datacenter > Storage > Add > Proxmox Backup Server).")
	case len(matches) == 0:
		return "", fiber.NewError(fiber.StatusBadRequest, "No enabled Proxmox storage on node "+node+" reads "+what+
			" (its root namespace). The PBS storages on this cluster are "+strings.Join(pbs, ", ")+
			". Add one in Proxmox (Datacenter > Storage > Add > Proxmox Backup Server), with the address or fingerprint Nexara knows the PBS server by.")
	case len(matches) > 1:
		fix := "remove the duplicate, or restrict one of them to other nodes"
		if datastore == "" {
			fix = "name the datastore in the request to choose between them"
		}
		return "", fiber.NewError(fiber.StatusConflict, "More than one Proxmox storage on this cluster reads "+what+": "+
			strings.Join(matches, ", ")+". A restore cannot tell which to read through; "+fix+".")
	}
	return matches[0], nil
}

func quoted(s string) string { return `"` + s + `"` }

func restoreStorageMatches(cfg proxmox.StorageConfig, server db.PbsServer, datastore, node string) bool {
	if cfg.Disable != 0 || cfg.Namespace != "" {
		return false
	}
	if cfg.Nodes != "" && !slices.Contains(nodeList(cfg.Nodes), node) {
		return false
	}
	if datastore != "" && cfg.Datastore != datastore {
		return false
	}
	return storageIsServer(cfg, server)
}

// nodeList splits a storage's "nodes" the way Proxmox reads one (split_list:
// commas, semicolons and white space all separate).
func nodeList(nodes string) []string {
	return strings.FieldsFunc(nodes, func(r rune) bool { return r == ',' || r == ';' || unicode.IsSpace(r) })
}

// storageIsServer reports whether a pbs storage points at a Nexara PBS server:
// the same host, or the same certificate fingerprint when both carry one. The
// fingerprint is the way round a server that Nexara and Proxmox reach by
// different names. The port is not compared (Proxmox's config carries it, this
// client's StorageConfig does not), so two PBS instances on one host cannot be
// told apart by address. Proxmox keeps an IPv6 server bare in its config and
// adds the brackets itself, while a URL has them.
func storageIsServer(cfg proxmox.StorageConfig, server db.PbsServer) bool {
	if fp := normalizeFingerprint(cfg.FingerprintPBS); fp != "" && fp == normalizeFingerprint(server.TlsFingerprint) {
		return true
	}
	u, err := url.Parse(server.ApiUrl)
	if err != nil || u.Hostname() == "" {
		return false
	}
	return strings.EqualFold(cfg.Server, u.Hostname())
}

func normalizeFingerprint(fp string) string {
	return strings.ToLower(strings.NewReplacer(":", "", " ", "").Replace(fp))
}
