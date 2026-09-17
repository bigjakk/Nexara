package apischema

import (
	"errors"
	"fmt"
	"math"
	"net"
	"net/mail"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// FormatFunc validates one string value and returns its normalized form.
// Normalization is the half that PVE's register_format gives us for free
// and that hand-rolled checks never do: "500G" and "500" and the JSON
// number 500 all have to reach the Proxmox volume spec as the bare GiB
// count "500", and the only reliable place to make that true is the
// moment the value is validated.
type FormatFunc func(value string) (normalized string, err error)

var (
	formatMu sync.RWMutex
	formats  = map[string]FormatFunc{}
)

// RegisterFormat registers fn under name. It panics on a duplicate name
// or an empty/nil argument: formats are registered from init functions
// and a collision is a programming error, not a runtime condition.
func RegisterFormat(name string, fn FormatFunc) {
	if name == "" {
		panic("apischema: RegisterFormat with an empty name")
	}
	if fn == nil {
		panic(fmt.Sprintf("apischema: RegisterFormat(%q) with a nil function", name))
	}
	formatMu.Lock()
	defer formatMu.Unlock()
	if _, dup := formats[name]; dup {
		panic(fmt.Sprintf("apischema: format %q is already registered", name))
	}
	formats[name] = fn
}

// LookupFormat returns the format registered under name.
func LookupFormat(name string) (FormatFunc, bool) {
	formatMu.RLock()
	defer formatMu.RUnlock()
	fn, ok := formats[name]
	return fn, ok
}

// MaxDiskSizeGiB caps the disk-size format at 1 PiB expressed in GiB.
// Proxmox itself imposes no such ceiling; this one exists so that a
// fat-fingered size cannot ask a storage backend to allocate an absurd
// volume, and it is exported so callers can quote it in their own errors.
const MaxDiskSizeGiB = 1024 * 1024

// diskSizeRe matches a decimal size with an optional binary unit. The
// unit letter is required before any "B"/"iB" suffix, so that "500B"
// (500 bytes) cannot be misread as 500 GiB.
var diskSizeRe = regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*(?:([KMGTPkmgtp])[iI]?[bB]?)?$`)

// diskSizeUnits maps a unit letter to its size in GiB.
var diskSizeUnits = map[byte]float64{
	'K': 1.0 / (1024 * 1024),
	'M': 1.0 / 1024,
	'G': 1,
	'T': 1024,
	'P': 1024 * 1024,
}

var (
	storageIDRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]*$`)
	nodeNameRe  = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)
	configIDRe  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{1,39}$`)
	uuidRe      = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	fpColonRe   = regexp.MustCompile(`^[0-9a-fA-F]{2}(?::[0-9a-fA-F]{2}){31}$`)
	fpBareRe    = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	nonNegIntRe = regexp.MustCompile(`^\d+$`)

	// emailLocalRe is RFC 5322's dot-atom: the local parts that survive a
	// round trip unquoted. mail.ParseAddress also accepts a QUOTED local
	// part and hands back the unquoted text, so `"a,b"@example.com`
	// normalizes to a,b@example.com — which this package would then
	// reject, and which any comma-joined recipient list would read as two
	// addresses. Refusing them keeps normalization a fixed point.
	emailLocalRe = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+/=?^_`{|}~-]+(?:\\.[A-Za-z0-9!#$%&'*+/=?^_`{|}~-]+)*$")
)

// Rejection messages that more than one path returns, or that would
// otherwise be built on every call including the successful ones.
var (
	errEmptyForm    = errors.New("value must not be empty")
	errDiskSize     = errors.New("expected a disk size such as 500, 500G or 1T")
	errDiskSizeZero = errors.New("disk size must be greater than zero")
	errDiskSizeMax  = fmt.Errorf("disk size must not exceed %d GiB (1 PiB)", MaxDiskSizeGiB)
	errEmail        = errors.New("expected an email address such as user@example.com")
)

func init() {
	RegisterFormat("disk-size", formatDiskSize)
	RegisterFormat("storage-id", formatStorageID)
	RegisterFormat("node-name", formatNodeName)
	RegisterFormat("pve-configid", formatConfigID)
	RegisterFormat("uuid", formatUUID)
	RegisterFormat("email", formatEmail)
	RegisterFormat("ip", formatIP)
	RegisterFormat("cidr", formatCIDR)
	RegisterFormat("mac-addr", formatMACAddr)
	RegisterFormat("fingerprint-sha256", formatFingerprintSHA256)
	RegisterFormat("bwlimit", formatBWLimit)
}

// formatDiskSize normalizes a disk size to the bare GiB count that
// Proxmox's "storage:N" allocation form expects.
//
// A value with no unit is already GiB — "512000" is 512000 GiB, not
// 512000 MiB; a caller who means MiB must write "512000M". Every unit is
// binary (G and GB alike mean GiB), matching Proxmox. Anything under a
// whole GiB rounds UP, because rounding down turns "512M" into a
// zero-sized allocation request.
//
// A per-schema cap does NOT go through Minimum/Maximum: the property is a
// string, so a numeric bound on it is a declaration error (a startup
// failure once the router compiles its schemas, and a 500 until then).
// Cap it with MaxLength on the normalized value instead, which counts
// digits and therefore caps at a power of ten — MaxLength: 4 allows up to
// 9999 GiB. A real capacity check belongs against the target pool's own
// free space, not against a constant in the schema.
func formatDiskSize(value string) (string, error) {
	m := diskSizeRe.FindStringSubmatch(strings.TrimSpace(value))
	if m == nil {
		return "", errDiskSize
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
		// ParseFloat only fails here on an out-of-range magnitude; the
		// regex has already ruled out every non-numeric shape.
		return "", errDiskSizeMax
	}
	unit := byte('G')
	if m[2] != "" {
		unit = strings.ToUpper(m[2])[0]
	}
	gib := n * diskSizeUnits[unit]
	if gib <= 0 {
		return "", errDiskSizeZero
	}
	gib = math.Ceil(gib)
	if gib > MaxDiskSizeGiB {
		return "", errDiskSizeMax
	}
	return strconv.FormatInt(int64(gib), 10), nil
}

// formatStorageID validates a Proxmox storage pool id.
func formatStorageID(value string) (string, error) {
	if value == "" {
		return "", errEmptyForm
	}
	if len(value) > 100 || !storageIDRe.MatchString(value) {
		return "", errors.New("expected a storage id starting with a letter, such as store01 (letters, digits, dot, underscore and dash, up to 100 characters)")
	}
	return value, nil
}

// formatNodeName validates a Proxmox node name. Three incompatible copies
// of this check exist elsewhere in the tree; this is the definition they
// are meant to converge on.
func formatNodeName(value string) (string, error) {
	if value == "" {
		return "", errEmptyForm
	}
	if len(value) > 63 || !nodeNameRe.MatchString(value) {
		return "", errors.New("expected a node name such as pve-01 (letters, digits, dot and dash, up to 63 characters)")
	}
	return value, nil
}

// formatConfigID validates PVE's configid shape, used for snapshot names
// and similar configuration keys.
func formatConfigID(value string) (string, error) {
	if value == "" {
		return "", errEmptyForm
	}
	if !configIDRe.MatchString(value) {
		return "", errors.New("expected an identifier starting with a letter, 2 to 40 characters long (letters, digits, underscore and dash)")
	}
	return value, nil
}

// formatUUID accepts the canonical 8-4-4-4-12 form in either case and
// normalizes it to lowercase. The braced and urn: forms that some UUID
// parsers accept are rejected on purpose: they never round-trip through
// our own URLs.
func formatUUID(value string) (string, error) {
	if !uuidRe.MatchString(value) {
		return "", errors.New("expected a UUID such as 3f2504e0-4f89-11d3-9a0c-0305e82c3301")
	}
	return strings.ToLower(value), nil
}

// formatEmail accepts a bare address (optionally in angle brackets) and
// normalizes it to the address itself. A display name is rejected: these
// values are used as recipients and as identifiers, and silently dropping
// half of what the caller sent is worse than refusing it. So is a quoted
// local part — see emailLocalRe.
func formatEmail(value string) (string, error) {
	addr, err := mail.ParseAddress(strings.TrimSpace(value))
	if err != nil || addr.Name != "" {
		return "", errEmail
	}
	at := strings.LastIndex(addr.Address, "@")
	if at < 0 || !strings.Contains(addr.Address[at+1:], ".") {
		return "", errEmail
	}
	if !emailLocalRe.MatchString(addr.Address[:at]) {
		return "", errEmail
	}
	return addr.Address, nil
}

// formatIP validates an IPv4 or IPv6 address and returns its canonical
// text form (IPv6 lowercased and zero-compressed).
func formatIP(value string) (string, error) {
	addr, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil {
		return "", errors.New("expected an IP address such as 192.0.2.10")
	}
	return addr.String(), nil
}

// formatCIDR validates an address with a prefix length. Host bits are
// allowed and preserved, as Proxmox interface configuration uses them.
func formatCIDR(value string) (string, error) {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
	if err != nil {
		return "", errors.New("expected a network in CIDR form such as 192.0.2.0/24")
	}
	return prefix.String(), nil
}

// formatMACAddr accepts the colon, dash and Cisco dotted forms of an
// EUI-48 address and normalizes to lowercase colon-separated.
func formatMACAddr(value string) (string, error) {
	hw, err := net.ParseMAC(strings.TrimSpace(value))
	if err != nil || len(hw) != 6 {
		return "", errors.New("expected a MAC address such as 02:00:00:00:00:01")
	}
	return hw.String(), nil
}

// formatFingerprintSHA256 accepts 32 colon-separated hex byte pairs or 64
// bare hex characters and normalizes to uppercase colon-separated pairs,
// which is how Proxmox prints them.
func formatFingerprintSHA256(value string) (string, error) {
	v := strings.TrimSpace(value)
	switch {
	case fpColonRe.MatchString(v):
		return strings.ToUpper(v), nil
	case fpBareRe.MatchString(v):
		v = strings.ToUpper(v)
		pairs := make([]string, 0, 32)
		for i := 0; i < len(v); i += 2 {
			pairs = append(pairs, v[i:i+2])
		}
		return strings.Join(pairs, ":"), nil
	default:
		return "", errors.New("expected a SHA-256 fingerprint as 32 colon-separated hex pairs or 64 hex characters")
	}
}

// formatBWLimit validates a non-negative bandwidth limit in KiB/s. Zero
// is meaningful to Proxmox — it means "no limit" — so it is accepted.
func formatBWLimit(value string) (string, error) {
	v := strings.TrimSpace(value)
	if !nonNegIntRe.MatchString(v) {
		return "", errors.New("expected a non-negative bandwidth limit in KiB/s, such as 10240")
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return "", errors.New("bandwidth limit is too large")
	}
	return strconv.FormatInt(n, 10), nil
}
