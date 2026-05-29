package proxmox

import (
	"strconv"
	"strings"
)

// CRSSettings holds the parsed Proxmox Cluster Resource Scheduler config from
// the datacenter.cfg `crs` property-string, e.g.:
//
//	ha=dynamic,ha-auto-rebalance=1,ha-auto-rebalance-threshold=30
//
// The dynamic scheduler and the ha-auto-rebalance* keys are Proxmox VE 9.2
// additions — the native "Dynamic Load Balancer."
type CRSSettings struct {
	HA               string // basic | static | dynamic
	AutoRebalance    bool   // ha-auto-rebalance
	Threshold        int    // ha-auto-rebalance-threshold (0-100, default 30)
	HoldDuration     int    // ha-auto-rebalance-hold-duration (HA rounds, default 3)
	Margin           int    // ha-auto-rebalance-margin (0-100, default 10)
	Method           string // ha-auto-rebalance-method: bruteforce | topsis
	RebalanceOnStart bool   // ha-rebalance-on-start
	Raw              string // original property-string (empty when decoded from an object)
}

// AutoRebalanceActive reports whether Proxmox's native CRS dynamic load balancer
// will actively migrate guests — the condition that conflicts with Nexara DRS
// automatic mode.
func (s CRSSettings) AutoRebalanceActive() bool {
	return s.AutoRebalance
}

// ParseCRSSettings parses the `crs` value from GET /cluster/options. Proxmox
// returns it as a property-string, but some round-trips decode it into an
// object; accept both shapes (mirrors the frontend's tolerant handling of the
// migration/ha options).
func ParseCRSSettings(v interface{}) CRSSettings {
	var s CRSSettings
	switch val := v.(type) {
	case string:
		s.Raw = val
		assignCRS(splitCRSPropString(val), &s)
	case map[string]interface{}:
		m := make(map[string]string, len(val))
		for k, vv := range val {
			m[k] = crsValueToString(vv)
		}
		assignCRS(m, &s)
	}
	return s
}

// splitCRSPropString turns "k1=v1,k2=v2" into a map. Bare tokens (no "=") are
// ignored.
func splitCRSPropString(raw string) map[string]string {
	m := make(map[string]string)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, val, found := strings.Cut(part, "=")
		if !found {
			continue
		}
		m[strings.TrimSpace(k)] = strings.TrimSpace(val)
	}
	return m
}

func assignCRS(m map[string]string, s *CRSSettings) {
	s.HA = m["ha"]
	s.Method = m["ha-auto-rebalance-method"]
	s.AutoRebalance = crsBool(m["ha-auto-rebalance"])
	s.RebalanceOnStart = crsBool(m["ha-rebalance-on-start"])
	s.Threshold = crsInt(m["ha-auto-rebalance-threshold"])
	s.HoldDuration = crsInt(m["ha-auto-rebalance-hold-duration"])
	s.Margin = crsInt(m["ha-auto-rebalance-margin"])
}

func crsBool(s string) bool {
	return s == "1" || strings.EqualFold(s, "true")
}

func crsInt(s string) int {
	if s == "" {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// crsValueToString normalizes a JSON-decoded value (string/number/bool) to the
// string form used in the property-string.
func crsValueToString(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		if t {
			return "1"
		}
		return "0"
	default:
		return ""
	}
}
