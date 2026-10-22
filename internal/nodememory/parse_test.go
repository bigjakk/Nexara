package nodememory

import (
	"reflect"
	"strings"
	"testing"
)

// Synthetic dmidecode output in the shapes real firmware produces. The part
// numbers and serials are placeholders: real output carries hardware serials,
// which must never land in the repo.
const (
	// dmidecode 3.x on a two-slot-per-channel server board, with a flash
	// array that is not system memory.
	serverDDR4 = `# dmidecode 3.4
Getting SMBIOS data from sysfs.
SMBIOS 3.2.0 present.

Handle 0x1000, DMI type 16, 23 bytes
Physical Memory Array
	Location: System Board Or Motherboard
	Use: System Memory
	Error Correction Type: Multi-bit ECC
	Maximum Capacity: 1 TB
	Error Information Handle: Not Provided
	Number Of Devices: 3

Handle 0x1001, DMI type 16, 23 bytes
Physical Memory Array
	Location: System Board Or Motherboard
	Use: Flash Memory
	Error Correction Type: None
	Maximum Capacity: 16 MB
	Number Of Devices: 1

Handle 0x1100, DMI type 17, 84 bytes
Memory Device
	Array Handle: 0x1000
	Total Width: 72 bits
	Data Width: 64 bits
	Size: 32 GB
	Form Factor: DIMM
	Locator: A1
	Bank Locator: Not Specified
	Type: DDR4
	Type Detail: Synchronous Registered (Buffered)
	Speed: 2933 MT/s
	Manufacturer: Samsung
	Serial Number: 00000000
	Asset Tag: 00000000
	Part Number: TEST-PART-0001
	Rank: 2
	Configured Memory Speed: 2666 MT/s

Handle 0x1101, DMI type 17, 84 bytes
Memory Device
	Array Handle: 0x1000
	Size: No Module Installed
	Form Factor: DIMM
	Locator: A2
	Bank Locator: Not Specified
	Type: Unknown
	Speed: Unknown
	Manufacturer: Not Specified
	Serial Number: Not Specified
	Part Number: Not Specified
	Rank: Unknown
	Configured Memory Speed: Unknown

Handle 0x1102, DMI type 17, 84 bytes
Memory Device
	Array Handle: 0x1000
	Size: 32 GB
	Form Factor: DIMM
	Locator: B1
	Type: DDR4
	Speed: 2933 MT/s
	Manufacturer: Samsung
	Part Number: TEST-PART-0001
	Rank: 2
	Configured Memory Speed: 2666 MT/s

Handle 0x1103, DMI type 17, 40 bytes
Memory Device
	Array Handle: 0x1001
	Size: 16 MB
	Form Factor: Chip
	Locator: SPI flash
	Type: Flash
`

	// dmidecode 2.x: sizes in MB, speeds in MHz, "Configured Clock Speed",
	// and two sockets' arrays with different ECC.
	olderTwoSocket = `# dmidecode 2.12
Handle 0x0020, DMI type 16, 23 bytes
Physical Memory Array
	Use: System Memory
	Error Correction Type: Single-bit ECC
	Maximum Capacity: 384 GB
	Number Of Devices: 1

Handle 0x0021, DMI type 16, 23 bytes
Physical Memory Array
	Use: System Memory
	Error Correction Type: Multi-bit ECC
	Maximum Capacity: 384 GB
	Number Of Devices: 1

Handle 0x0030, DMI type 17, 34 bytes
Memory Device
	Array Handle: 0x0020
	Size: 16384 MB
	Locator: PROC 1 DIMM 1
	Type: DDR3
	Speed: 1600 MHz
	Configured Clock Speed: 1333 MHz

Handle 0x0031, DMI type 17, 34 bytes
Memory Device
	Array Handle: 0x0021
	Size: 8192 MB
	Locator: PROC 2 DIMM 1
	Type: DDR3
	Speed: 1600 MHz
	Configured Clock Speed: 1333 MHz
`

	// Firmware that leaves the array's Use as Other.
	otherUse = `Handle 0x0040, DMI type 16, 23 bytes
Physical Memory Array
	Use: Other
	Maximum Capacity: 64 GB

Handle 0x0041, DMI type 17, 40 bytes
Memory Device
	Array Handle: 0x0040
	Size: 8 GB
	Locator: DIMM1
	Type: DDR4
`

	// A Proxmox node that is itself a QEMU guest.
	qemuGuest = `Handle 0x1000, DMI type 16, 23 bytes
Physical Memory Array
	Location: Other
	Use: System Memory
	Error Correction Type: Multi-bit ECC
	Maximum Capacity: 16 GB
	Number Of Devices: 1

Handle 0x1100, DMI type 17, 40 bytes
Memory Device
	Array Handle: 0x1000
	Size: 16 GB
	Form Factor: DIMM
	Locator: DIMM 0
	Bank Locator: Not Specified
	Type: RAM
	Speed: Unknown
	Manufacturer: QEMU
	Part Number: Not Specified
	Rank: Unknown
`
)

// Parse leaves out only arrays known not to be system memory, reads sizes and
// speeds in either dmidecode's current or its 2.x spelling, blanks firmware
// placeholders, turns "No Module Installed" into an empty slot, and refuses
// output with more structures than a memory layout has.
func TestParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                string
		out                 string
		wantMax             int64
		wantECC             string
		wantInstalled       int64
		wantUsed, wantSlots int
		wantFirst           Module
		wantErr             bool
	}{
		{
			name: "server DDR4 with an empty slot and a flash array", out: serverDDR4,
			wantMax: 1 << 40, wantECC: "Multi-bit ECC", wantInstalled: 64 << 30, wantUsed: 2, wantSlots: 3,
			wantFirst: Module{Locator: "A1", SizeBytes: 32 << 30, Type: "DDR4", FormFactor: "DIMM", SpeedMTs: 2933, ConfiguredSpeedMTs: 2666, Manufacturer: "Samsung", PartNumber: "TEST-PART-0001", Rank: 2},
		},
		{
			name: "dmidecode 2.x on two sockets", out: olderTwoSocket,
			wantMax: 768 << 30, wantECC: "mixed", wantInstalled: 24 << 30, wantUsed: 2, wantSlots: 2,
			wantFirst: Module{Locator: "PROC 1 DIMM 1", SizeBytes: 16 << 30, Type: "DDR3", SpeedMTs: 1600, ConfiguredSpeedMTs: 1333},
		},
		{
			name: "a node that is a QEMU guest", out: qemuGuest,
			wantMax: 16 << 30, wantECC: "Multi-bit ECC", wantInstalled: 16 << 30, wantUsed: 1, wantSlots: 1,
			wantFirst: Module{Locator: "DIMM 0", SizeBytes: 16 << 30, Type: "RAM", FormFactor: "DIMM", Manufacturer: "QEMU"},
		},
		{
			name: "firmware that leaves Use as Other", out: otherUse,
			wantMax: 64 << 30, wantInstalled: 8 << 30, wantUsed: 1, wantSlots: 1,
			wantFirst: Module{Locator: "DIMM1", SizeBytes: 8 << 30, Type: "DDR4"},
		},
		{
			name: "control characters in a field are dropped", out: "Handle 0x1100, DMI type 17, 40 bytes\nMemory Device\n\tSize: 8 GB\n\tLocator: DIMM\x000\n\tPart Number: TEST\x07PART\n",
			wantInstalled: 8 << 30, wantUsed: 1, wantSlots: 1,
			wantFirst: Module{Locator: "DIMM0", SizeBytes: 8 << 30, PartNumber: "TESTPART"},
		},
		{name: "no SMBIOS at all", out: "# dmidecode 3.4\n# No SMBIOS nor DMI entry point found, sorry.\n"},
		{name: "more structures than a memory layout has", out: strings.Repeat("Handle 0x1100, DMI type 17, 40 bytes\nMemory Device\n\tSize: 8 GB\n\n", maxBlocks+1), wantErr: true},
		{name: "empty output", out: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			inv, err := Parse(tt.out)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want an error: %v", err, tt.wantErr)
			}
			if inv.MaxCapacityBytes != tt.wantMax || inv.ECC != tt.wantECC || inv.InstalledBytes() != tt.wantInstalled || inv.SlotsUsed() != tt.wantUsed || len(inv.Modules) != tt.wantSlots {
				t.Errorf("max %d ECC %q installed %d used %d slots %d; want %d %q %d %d %d",
					inv.MaxCapacityBytes, inv.ECC, inv.InstalledBytes(), inv.SlotsUsed(), len(inv.Modules),
					tt.wantMax, tt.wantECC, tt.wantInstalled, tt.wantUsed, tt.wantSlots)
			}
			if tt.wantSlots > 0 && !reflect.DeepEqual(inv.Modules[0], tt.wantFirst) {
				t.Errorf("first module = %+v, want %+v", inv.Modules[0], tt.wantFirst)
			}
		})
	}
}
