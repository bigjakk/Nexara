// Package nodememory reads a node's memory DIMM layout, which Proxmox exposes
// through no API, from `dmidecode` over the cluster's SSH credentials, and
// keeps the last good read per node.
package nodememory

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Command is the one command the reader runs on a node: the SMBIOS physical
// memory arrays (type 16) and memory devices (type 17). It needs root. The
// path is absolute because Debian gives a non-root login a PATH without
// /usr/sbin, which would report "not installed" where the truth is "needs root".
const Command = "/usr/sbin/dmidecode -t 16 -t 17"

// MaxOutput caps what a read keeps of dmidecode's output. A board with a few
// hundred sockets prints well under 200 KiB; the cap is what keeps a node that
// streams without end from growing Nexara's memory.
const MaxOutput = 1 << 20

const (
	// maxBlocks bounds the structures one read may hold. Past it the output
	// is not a memory layout, and nothing of it is stored.
	maxBlocks = 1024
	// maxValueLen truncates a field: locators and part numbers are short.
	maxValueLen = 128
)

// errTooManyBlocks reports output with more structures than maxBlocks.
var errTooManyBlocks = errors.New("dmidecode printed more structures than a memory layout has")

// Module is one memory device socket. Serial numbers and asset tags are
// dropped on purpose: they identify the operator's hardware and no figure
// needs them.
type Module struct {
	Locator            string `json:"locator"`
	BankLocator        string `json:"bank_locator,omitempty"`
	SizeBytes          int64  `json:"size_bytes"` // 0: no module installed
	Type               string `json:"type,omitempty"`
	FormFactor         string `json:"form_factor,omitempty"`
	SpeedMTs           int    `json:"speed_mts,omitempty"`
	ConfiguredSpeedMTs int    `json:"configured_speed_mts,omitempty"`
	Manufacturer       string `json:"manufacturer,omitempty"`
	PartNumber         string `json:"part_number,omitempty"`
	Rank               int    `json:"rank,omitempty"`
}

// Inventory is what one read found.
type Inventory struct {
	// MaxCapacityBytes is the firmware's own "Maximum Capacity", summed over
	// the system memory arrays. Boards often understate or overstate it, so
	// it is shown as the firmware's figure, not as a fact.
	MaxCapacityBytes int64
	// ECC is the arrays' error correction type ("Multi-bit ECC", "None"), or
	// "mixed" when they differ.
	ECC     string
	Modules []Module
}

// InstalledBytes is the sum of the installed modules.
func (inv Inventory) InstalledBytes() int64 {
	var total int64
	for _, m := range inv.Modules {
		total += m.SizeBytes
	}
	return total
}

// SlotsUsed counts the sockets holding a module.
func (inv Inventory) SlotsUsed() int {
	n := 0
	for _, m := range inv.Modules {
		if m.SizeBytes > 0 {
			n++
		}
	}
	return n
}

type dmiBlock struct {
	handle string
	typ    int
	fields map[string]string
}

// Parse reads `dmidecode -t 16 -t 17` output. Devices on an array known not to
// be system memory (flash, video, cache, non-volatile) are left out; a device
// whose array is not listed, or whose firmware leaves Use as Other or Unknown,
// is kept. Output with no memory device gives an empty inventory, which the
// caller reports; output that is not a memory layout is an error.
func Parse(out string) (Inventory, error) {
	blocks, err := splitBlocks(out)
	if err != nil {
		return Inventory{}, err
	}
	arrays := make(map[string]dmiBlock)
	for _, b := range blocks {
		if b.typ == 16 {
			arrays[b.handle] = b
		}
	}

	var inv Inventory
	eccSeen := ""
	for _, b := range blocks {
		if b.typ != 16 || !systemMemory(b.fields["Use"]) {
			continue
		}
		inv.MaxCapacityBytes += parseSize(b.fields["Maximum Capacity"])
		ecc := known(b.fields["Error Correction Type"])
		switch {
		case ecc == "":
		case eccSeen == "":
			eccSeen = ecc
		case eccSeen != ecc:
			eccSeen = "mixed"
		}
	}
	inv.ECC = eccSeen

	for _, b := range blocks {
		if b.typ != 17 {
			continue
		}
		if a, ok := arrays[b.fields["Array Handle"]]; ok && !systemMemory(a.fields["Use"]) {
			continue
		}
		configured := b.fields["Configured Memory Speed"]
		if configured == "" {
			configured = b.fields["Configured Clock Speed"] // dmidecode before 3.0
		}
		inv.Modules = append(inv.Modules, Module{
			Locator:            known(b.fields["Locator"]),
			BankLocator:        known(b.fields["Bank Locator"]),
			SizeBytes:          parseSize(b.fields["Size"]),
			Type:               known(b.fields["Type"]),
			FormFactor:         known(b.fields["Form Factor"]),
			SpeedMTs:           parseLeadingInt(b.fields["Speed"]),
			ConfiguredSpeedMTs: parseLeadingInt(configured),
			Manufacturer:       known(b.fields["Manufacturer"]),
			PartNumber:         known(b.fields["Part Number"]),
			Rank:               parseLeadingInt(b.fields["Rank"]),
		})
	}
	return inv, nil
}

// systemMemory says whether an array's Use leaves it counted. SMBIOS names
// seven uses; four are known not to be the node's memory, and firmware that
// leaves the field Other, Unknown or empty is given the benefit of the doubt.
func systemMemory(use string) bool {
	switch use {
	case "Video Memory", "Flash Memory", "Cache Memory", "Non-volatile RAM":
		return false
	}
	return true
}

// splitBlocks cuts the output into its "Handle 0x…, DMI type N" blocks and
// keeps each block's one-tab "Key: Value" lines. Deeper-indented lines are
// list continuations no field here needs.
func splitBlocks(out string) ([]dmiBlock, error) {
	var blocks []dmiBlock
	var cur *dmiBlock
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.HasPrefix(line, "Handle ") {
			if len(blocks) == maxBlocks {
				return nil, errTooManyBlocks
			}
			blocks = append(blocks, dmiBlock{fields: map[string]string{}})
			cur = &blocks[len(blocks)-1]
			// "Handle 0x1100, DMI type 17, 84 bytes"
			parts := strings.Split(strings.TrimPrefix(line, "Handle "), ",")
			cur.handle = strings.TrimSpace(parts[0])
			if len(parts) > 1 {
				cur.typ = parseLeadingInt(strings.TrimPrefix(strings.TrimSpace(parts[1]), "DMI type "))
			}
			continue
		}
		if cur == nil || !strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "\t\t") {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimPrefix(line, "\t"), ":")
		if !ok {
			continue
		}
		// Control characters are dropped (real dmidecode prints them as "."):
		// a NUL survives json.Marshal as \u0000, which jsonb refuses, and
		// would make the read unstorable.
		value = strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7f {
				return -1
			}
			return r
		}, strings.TrimSpace(value))
		if len(value) > maxValueLen {
			value = value[:maxValueLen]
		}
		cur.fields[strings.TrimSpace(key)] = value
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read dmidecode output: %w", err)
	}
	return blocks, nil
}

// known blanks the placeholders firmware writes for an empty field.
func known(v string) string {
	switch strings.ToLower(v) {
	case "", "unknown", "not specified", "not provided", "none of the above", "<out of spec>", "to be filled by o.e.m.":
		return ""
	}
	return v
}

// parseSize reads "32 GB", "16384 MB", "512 kB" or "1 TB" (dmidecode's binary
// units) into bytes; "No Module Installed", "Unknown" and the like are 0.
func parseSize(v string) int64 {
	num, unit, ok := strings.Cut(strings.TrimSpace(v), " ")
	if !ok {
		return 0
	}
	n, err := strconv.ParseInt(num, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	shift, ok := map[string]uint{"bytes": 0, "kB": 10, "MB": 20, "GB": 30, "TB": 40}[unit]
	if !ok || n > math.MaxInt64>>shift {
		return 0
	}
	return n << shift
}

// parseLeadingInt reads the number at the start of "3200 MT/s", "2133 MHz" or
// "2"; anything else, "Unknown" included, is 0.
func parseLeadingInt(v string) int {
	v = strings.TrimSpace(v)
	end := 0
	for end < len(v) && v[end] >= '0' && v[end] <= '9' {
		end++
	}
	n, err := strconv.Atoi(v[:end])
	if err != nil {
		return 0
	}
	return n
}
