package proxmox

import (
	"encoding/json"
	"testing"
)

// The JSON in this file is the shape PVE actually emits, taken from the
// published API schema (pve-docs/api-viewer/apidoc.js) and, for the SMART
// attribute rows the schema leaves as an untyped array, from the producer
// itself: pve-storage's PVE/Diskmanage.pm get_smart_data.
//
// Each test here fails against the struct definitions that shipped before
// it, with these errors verbatim — quoted exactly so a future reader can
// grep a 500's log line and land back here:
//
//	json: cannot unmarshal string into Go struct field DiskSMARTData.attributes.0.id of type int
//	json: cannot unmarshal number into Go struct field .0.port of type string
//
// Both endpoints answered 500 on every call for that reason.

// TestSMARTAttributeDecodesPVEShape pins the mixed number/string shape of a
// single ATA attribute row.
//
// The padding on "id" is the load-bearing part. Diskmanage.pm matches the
// ID# column as ([ \d]{2}\d) and assigns the capture verbatim, so a
// single-digit id arrives as "  1", not "1". A FlexInt that fed that to
// strconv.Atoi without trimming would decode it as 0 — no error, no 500,
// just every attribute below 100 reported as id 0 — so this test is what
// separates a working fix from a silent one.
func TestSMARTAttributeDecodesPVEShape(t *testing.T) {
	t.Parallel()

	const payload = `{
		"health": "PASSED",
		"type": "ata",
		"attributes": [
			{"id": "  1", "name": "Raw_Read_Error_Rate", "flags": "POSR-K",
			 "value": 100, "worst": 100, "threshold": 0, "raw": "0"},
			{"id": " 12", "name": "Power_Cycle_Count", "flags": "-O--CK",
			 "value": 99, "worst": 99, "threshold": 0, "raw": "412"},
			{"id": "194", "name": "Temperature_Celsius", "flags": "-O---K",
			 "value": 64, "worst": 45, "threshold": 0, "raw": "36"}
		]
	}`

	var got DiskSMARTData
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatalf("decoding PVE's SMART payload: %v", err)
	}

	if got.Health != "PASSED" || got.Type != "ata" {
		t.Fatalf("health/type = %q/%q, want PASSED/ata", got.Health, got.Type)
	}

	want := []SMARTAttribute{
		{ID: 1, Name: "Raw_Read_Error_Rate", Flags: "POSR-K", Value: 100, Worst: 100, Threshold: 0, Raw: "0"},
		{ID: 12, Name: "Power_Cycle_Count", Flags: "-O--CK", Value: 99, Worst: 99, Threshold: 0, Raw: "412"},
		{ID: 194, Name: "Temperature_Celsius", Flags: "-O---K", Value: 64, Worst: 45, Threshold: 0, Raw: "36"},
	}
	if len(got.Attributes) != len(want) {
		t.Fatalf("decoded %d attributes, want %d", len(got.Attributes), len(want))
	}
	for i, w := range want {
		if got.Attributes[i] != w {
			t.Errorf("attribute %d = %+v, want %+v", i, got.Attributes[i], w)
		}
	}
}

// TestSMARTAttributeIDMarshalsAsNumber pins the outward half of the contract.
// The SPA types id as `number` (SMARTAttributeResponse in
// frontend/src/features/clusters/api/cluster-queries.ts) and keys the table
// row on it, so a FlexInt that grew a MarshalJSON emitting a string would
// break the consumer even though the decode had been fixed.
func TestSMARTAttributeIDMarshalsAsNumber(t *testing.T) {
	t.Parallel()

	b, err := json.Marshal(SMARTAttribute{ID: 194, Name: "Temperature_Celsius"})
	if err != nil {
		t.Fatalf("marshalling attribute: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("re-reading marshalled attribute: %v", err)
	}
	if string(raw["id"]) != "194" {
		t.Errorf("id marshalled as %s, want the bare number 194", raw["id"])
	}
}

// TestNodeUSBDeviceDecodesPVEShape covers the opposite mismatch: PVE declares
// port as "type": "integer", and the struct read it as a string.
func TestNodeUSBDeviceDecodesPVEShape(t *testing.T) {
	t.Parallel()

	const payload = `[
		{"busnum": 1, "devnum": 2, "port": 3, "prodid": "0024", "vendid": "1d6b",
		 "product": "xHCI Host Controller", "manufacturer": "Linux Foundation",
		 "speed": "480", "class": 9, "usbpath": "1", "level": 1}
	]`

	var got []NodeUSBDevice
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatalf("decoding PVE's USB payload: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("decoded %d devices, want 1", len(got))
	}
	if got[0].Port != "3" {
		t.Errorf("port = %q, want %q", got[0].Port, "3")
	}
	if got[0].Busnum != 1 || got[0].Devnum != 2 || got[0].Class != 9 {
		t.Errorf("busnum/devnum/class = %d/%d/%d, want 1/2/9",
			got[0].Busnum, got[0].Devnum, got[0].Class)
	}
}

// TestNodeUSBDevicePortMarshalsAsString is the USB counterpart to the SMART
// marshal test: the SPA's NodeUSBDevice types port as `string`, and this
// endpoint has never successfully returned a body, so the first one it does
// return is the one that sets the contract.
func TestNodeUSBDevicePortMarshalsAsString(t *testing.T) {
	t.Parallel()

	b, err := json.Marshal(NodeUSBDevice{Port: "3"})
	if err != nil {
		t.Fatalf("marshalling device: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("re-reading marshalled device: %v", err)
	}
	if string(raw["port"]) != `"3"` {
		t.Errorf(`port marshalled as %s, want the quoted string "3"`, raw["port"])
	}
}

// TestFlexIntAcceptsPaddedNumericStrings exercises the trim directly, at the
// type rather than through a caller, so the reason the SMART fix works is
// stated somewhere that does not depend on smartctl's column layout.
func TestFlexIntAcceptsPaddedNumericStrings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  FlexInt
	}{
		{"bare number", `5`, 5},
		{"unpadded string", `"5"`, 5},
		{"leading spaces", `"  1"`, 1},
		{"one leading space", `" 12"`, 12},
		{"trailing space", `"7 "`, 7},
		{"negative, padded", `" -3"`, -3},
		{"non-numeric falls back to zero", `"default"`, 0},
		{"empty string falls back to zero", `""`, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got FlexInt
			if err := json.Unmarshal([]byte(tt.input), &got); err != nil {
				t.Fatalf("unmarshalling %s: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("FlexInt(%s) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}
