package handlers

import (
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/gofiber/fiber/v3"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// How restoreStorage tied the storage it chose to the PBS server.
const (
	matchedByFingerprint   = "fingerprint"
	matchedByAddress       = "address"
	matchedByOnlyCandidate = "only_candidate"
)

// defaultPBSPort is the port Proxmox connects to when a pbs storage names none,
// and the port a PBS listens on unless it is told otherwise.
const defaultPBSPort = 8007

// restoreStorage names the Proxmox storage a restore reads its archive through,
// and says how it is tied to the PBS server the snapshot lives on.
//
// A restore addresses the snapshot as "<storage>:backup/<type>/<id>/<time>", so
// the storage decides which PBS it is read from. It used to be the first pbs
// storage of the cluster, whatever server and datastore were asked for, which
// with datastores synced between servers restored from a PBS nobody chose.
//
// A candidate is an enabled pbs storage that is available on the node, has no
// namespace — Nexara lists a datastore's root namespace only — and names the
// datastore. storageTie decides which candidates are the server's: one match
// is used, several are refused. With none, a single candidate that nothing
// contradicts is used when no other Nexara PBS server claims it, which is how a
// PBS that Nexara reaches by an address and Proxmox by a name keeps working; the
// caller records and logs that. Anything else is refused with what to change: a
// restore must not choose between two readings of the request.
func restoreStorage(cfgs []proxmox.StorageConfig, server db.PbsServer, others []db.PbsServer, datastore, node string) (storage, matchedBy string, err error) {
	var pbs, matches []string
	matchedWith := map[string]string{}
	var open []proxmox.StorageConfig
	for _, cfg := range cfgs {
		if cfg.Type != "pbs" {
			continue
		}
		pbs = append(pbs, cfg.Storage)
		if !restoreCandidate(cfg, datastore, node) {
			continue
		}
		switch storageTie(cfg, server) {
		case tieFingerprint:
			matches = append(matches, cfg.Storage)
			matchedWith[cfg.Storage] = matchedByFingerprint
		case tieAddress:
			matches = append(matches, cfg.Storage)
			matchedWith[cfg.Storage] = matchedByAddress
		case tieNone:
			open = append(open, cfg)
		case tieContradicted:
		}
	}
	sort.Strings(pbs)
	sort.Strings(matches)

	what := "datastore " + quoted(datastore) + " of PBS server " + quoted(server.Name)
	switch {
	case len(pbs) == 0:
		return "", "", fiber.NewError(fiber.StatusBadRequest, "No PBS storage configured on this PVE cluster. Add the PBS server as storage in PVE first (Datacenter > Storage > Add > Proxmox Backup Server).")
	case len(matches) > 1:
		return "", "", fiber.NewError(fiber.StatusConflict, "More than one Proxmox storage on this cluster reads "+what+": "+
			strings.Join(matches, ", ")+". A restore cannot tell which to read through; remove the duplicate, or restrict one of them to other nodes.")
	case len(matches) == 1:
		return matches[0], matchedWith[matches[0]], nil
	case len(open) == 1 && !claimedByAnother(open[0], server, others):
		return open[0].Storage, matchedByOnlyCandidate, nil
	}
	return "", "", fiber.NewError(fiber.StatusBadRequest, "No enabled Proxmox storage on node "+node+" reads "+what+
		" (its root namespace) and can be tied to that server. The PBS storages on this cluster are "+strings.Join(pbs, ", ")+
		". Add one in Proxmox (Datacenter > Storage > Add > Proxmox Backup Server), with the address, port or fingerprint Nexara knows the PBS server by.")
}

func quoted(s string) string { return `"` + s + `"` }

// restoreCandidate is whether a pbs storage could hold the snapshot at all.
func restoreCandidate(cfg proxmox.StorageConfig, datastore, node string) bool {
	if cfg.Disable != 0 || cfg.Namespace != "" || cfg.Datastore != datastore {
		return false
	}
	return cfg.Nodes == "" || slices.Contains(nodeList(cfg.Nodes), node)
}

// claimedByAnother reports whether any Nexara PBS server other than server is
// tied to the storage.
func claimedByAnother(cfg proxmox.StorageConfig, server db.PbsServer, others []db.PbsServer) bool {
	return slices.ContainsFunc(others, func(o db.PbsServer) bool {
		if o.ID == server.ID {
			return false
		}
		t := storageTie(cfg, o)
		return t == tieFingerprint || t == tieAddress
	})
}

// nodeList splits a storage's "nodes" the way Proxmox reads one (split_list:
// commas, semicolons and white space all separate).
func nodeList(nodes string) []string {
	return strings.FieldsFunc(nodes, func(r rune) bool { return r == ',' || r == ';' || unicode.IsSpace(r) })
}

// tie is what a pbs storage's own settings say about whether it is a Nexara PBS
// server.
type tie int

const (
	// tieNone: nothing says it is, nothing says it is not. The same server can
	// be reached by an address in Nexara and a name in Proxmox.
	tieNone tie = iota
	tieFingerprint
	tieAddress
	// tieContradicted: it is not the server. Both sides carry a certificate
	// fingerprint and they differ, or it is the same host on another port.
	tieContradicted
)

// storageTie compares a pbs storage with a Nexara PBS server. Two fingerprints
// decide on their own: equal is the same server, different is not, whatever the
// addresses say. Otherwise the host (case-insensitive) and the port must both
// agree; both default to 8007 when they name none. Proxmox keeps an IPv6 server
// bare in its config and adds the brackets itself, while a URL has them.
func storageTie(cfg proxmox.StorageConfig, server db.PbsServer) tie {
	if fs, fp := normalizeFingerprint(cfg.FingerprintPBS), normalizeFingerprint(server.TlsFingerprint); fs != "" && fp != "" {
		if fs == fp {
			return tieFingerprint
		}
		return tieContradicted
	}
	u, err := url.Parse(server.ApiUrl)
	if err != nil || u.Hostname() == "" || !strings.EqualFold(cfg.Server, u.Hostname()) {
		return tieNone
	}
	port := defaultPBSPort
	if p := u.Port(); p != "" {
		if port, err = strconv.Atoi(p); err != nil {
			return tieNone
		}
	}
	storagePort := int(cfg.Port)
	if storagePort == 0 {
		storagePort = defaultPBSPort
	}
	if port != storagePort {
		return tieContradicted
	}
	return tieAddress
}

func normalizeFingerprint(fp string) string {
	return strings.ToLower(strings.NewReplacer(":", "", " ", "").Replace(fp))
}
