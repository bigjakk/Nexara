package apischema

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
)

// Standard options mirror PVE's register_standard_option /
// get_standard_option: a property that ~550 endpoints repeat is defined
// once, so that its description, format and bounds cannot drift between
// routes and the generated documentation says the same thing everywhere.
var (
	stdMu      sync.RWMutex
	stdOptions = map[string]Property{}
)

// RegisterStdOption registers p under name. It panics on a duplicate
// name, which — like a duplicate format — can only be a programming
// error, since registration happens from init functions.
//
// Mind the ordering if a package both registers options and declares
// schemas: Go runs a package's variable initializers BEFORE its init
// functions, so a package-level var in the same package that calls
// StdOption for an option that package registers here will panic. Either
// register from a package the schemas import (this one, or a dedicated
// options package), or build those schemas in init rather than in a var.
func RegisterStdOption(name string, p Property) {
	if name == "" {
		panic("apischema: RegisterStdOption with an empty name")
	}
	stdMu.Lock()
	defer stdMu.Unlock()
	if _, dup := stdOptions[name]; dup {
		panic(fmt.Sprintf("apischema: standard option %q is already registered", name))
	}
	stdOptions[name] = p.clone()
}

// StdOption returns the standard option registered under name. It panics
// if the name is unknown: standard options are read while building a
// route's schema, so an unknown name is a typo in the declaration that
// should stop the process, not a request that should fail.
//
// Calling it from a package-level var is safe for anything registered by
// a package you import — every built-in option below included, since an
// imported package is fully initialized first. It is NOT safe for an
// option your own package registers in its own init: variable
// initializers run before init functions, so the lookup would run first
// and panic. See RegisterStdOption.
//
// The returned Property is a deep copy, so callers may adjust it —
// p.AsOptional(), a tighter description — without affecting other routes.
func StdOption(name string) Property {
	stdMu.RLock()
	defer stdMu.RUnlock()
	p, ok := stdOptions[name]
	if !ok {
		panic(fmt.Sprintf("apischema: unknown standard option %q (registered: %s)", name, strings.Join(slices.Sorted(maps.Keys(stdOptions)), ", ")))
	}
	return p.clone()
}

func init() {
	RegisterStdOption("cluster-id", Property{
		Type:        String,
		Format:      "uuid",
		Source:      SourcePath,
		Typetext:    "<uuid>",
		Description: "Nexara cluster identifier.",
	})
	RegisterStdOption("vm-id", Property{
		Type:     String,
		Format:   "uuid",
		Source:   SourcePath,
		Typetext: "<uuid>",
		// Nexara's own row id, not the Proxmox VMID: the collector
		// deletes and re-inserts guest rows, so the two are different
		// identifiers and confusing them has bitten us before.
		Description: "Nexara VM identifier (not the Proxmox VMID).",
	})
	RegisterStdOption("ct-id", Property{
		Type:        String,
		Format:      "uuid",
		Source:      SourcePath,
		Typetext:    "<uuid>",
		Description: "Nexara container identifier (not the Proxmox VMID).",
	})
	RegisterStdOption("node-name", Property{
		Type:        String,
		Format:      "node-name",
		Typetext:    "<name>",
		Description: "Proxmox node name.",
	})
	RegisterStdOption("storage-id", Property{
		Type:        String,
		Format:      "storage-id",
		Typetext:    "<storage>",
		Description: "Proxmox storage pool identifier.",
	})
	RegisterStdOption("pbs-id", Property{
		Type:        String,
		Format:      "uuid",
		Typetext:    "<uuid>",
		Description: "Proxmox Backup Server identifier.",
	})
	RegisterStdOption("bwlimit", Property{
		// Declared as an integer rather than a string with the bwlimit
		// format, because every caller wants a number: the format exists
		// for declarations that carry a bandwidth limit as text.
		Type:        Integer,
		Optional:    true,
		Minimum:     Ptr(0.0),
		Typetext:    "<integer> (KiB/s, 0 for unlimited)",
		Description: "Bandwidth limit in KiB/s. 0 means no limit.",
	})
	RegisterStdOption("fingerprint", Property{
		Type:        String,
		Format:      "fingerprint-sha256",
		Optional:    true,
		Typetext:    "<SHA256 fingerprint>",
		Description: "Expected SHA-256 certificate fingerprint.",
	})
}
