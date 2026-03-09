package rolling

import (
	"fmt"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// GuestSnapshot describes a VM or container that was drained from a node.
type GuestSnapshot struct {
	VMID        int    `json:"vmid"`
	Name        string `json:"name"`
	Type        string `json:"type"`   // "qemu" or "lxc"
	Status      string `json:"status"`
	Passthrough bool   `json:"passthrough,omitempty"` // PCI/USB passthrough — cannot live-migrate
}

// DisabledHARule tracks an HA rule that was temporarily disabled during drain.
type DisabledHARule struct {
	Rule string `json:"rule"`
	Type string `json:"type"` // "node-affinity" or "resource-affinity"
}

// hasPassthrough checks if a VM config contains PCI passthrough (hostpci0..15)
// or USB passthrough (usb0..4) devices. VMs with passthrough cannot be live-migrated.
func hasPassthrough(config proxmox.VMConfig) bool {
	for i := 0; i <= 15; i++ {
		key := fmt.Sprintf("hostpci%d", i)
		if _, ok := config[key]; ok {
			return true
		}
	}
	for i := 0; i <= 4; i++ {
		key := fmt.Sprintf("usb%d", i)
		if val, ok := config[key]; ok {
			// USB "spice" is virtual redirection, not physical passthrough.
			if s, isStr := val.(string); isStr && s == "spice" {
				continue
			}
			return true
		}
	}
	return false
}
