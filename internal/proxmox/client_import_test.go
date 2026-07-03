package proxmox

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// TestGetImportMetadata_StripsStoragePrefix locks in the fix for the PVE "unable to parse
// directory volume name" failure: the import-metadata endpoint is storage-scoped by its URL
// path, so the volume query param must be the storage-RELATIVE volname, not the full volid.
// Callers pass the full volid (as the content API lists it); the client must strip the
// "<storage>:" prefix before the request.
func TestGetImportMetadata_StripsStoragePrefix(t *testing.T) {
	var gotVolume string
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve1/storage/synology/import-metadata": func(w http.ResponseWriter, r *http.Request) {
			gotVolume = r.URL.Query().Get("volume")
			jsonResponse(w, map[string]any{"type": "vm", "source": "import/x.ova"})
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	// Caller passes the full volid, exactly as the content listing returns it.
	if _, err := c.GetImportMetadata(context.Background(), "pve1", "synology", "synology:import/x.ova"); err != nil {
		t.Fatalf("GetImportMetadata: %v", err)
	}
	if gotVolume != "import/x.ova" {
		t.Errorf("volume sent to PVE = %q, want %q (storage prefix must be stripped)", gotVolume, "import/x.ova")
	}

	// An already-relative volname must pass through unchanged.
	if _, err := c.GetImportMetadata(context.Background(), "pve1", "synology", "import/x.ova"); err != nil {
		t.Fatalf("GetImportMetadata (relative): %v", err)
	}
	if gotVolume != "import/x.ova" {
		t.Errorf("relative volume mangled: got %q", gotVolume)
	}
}

func sampleMetadata() *ImportMetadata {
	return &ImportMetadata{
		Type:   "vm",
		Source: "esxi",
		CreateArgs: map[string]json.RawMessage{
			"name":    json.RawMessage(`"web01"`),
			"cores":   json.RawMessage(`4`),
			"sockets": json.RawMessage(`2`),
			"memory":  json.RawMessage(`8192`),
			"ostype":  json.RawMessage(`"l26"`),
			"bios":    json.RawMessage(`"ovmf"`),
			"scsihw":  json.RawMessage(`"virtio-scsi-pci"`),
			"smbios1": json.RawMessage(`"uuid=abc-123"`),
		},
		Disks: map[string]json.RawMessage{
			"scsi0":    json.RawMessage(`{"volid":"esxi:ha/ds/web01/web01.vmdk","size":42949672960,"image-format":"vmdk"}`),
			"scsi1":    json.RawMessage(`"esxi:ha/ds/web01/web01_1.vmdk"`),
			"efidisk0": json.RawMessage(`{"volid":"esxi:ha/ds/web01/web01.nvram"}`),
		},
		Net: map[string]json.RawMessage{
			"net0": json.RawMessage(`{"model":"vmxnet3","macaddr":"AA:BB:CC:DD:EE:FF"}`),
		},
	}
}

func TestBuildImportCreateParams_MapsTypedFieldsAndDisks(t *testing.T) {
	meta := sampleMetadata()
	p := BuildImportCreateParams(meta, ImportCreateOptions{
		VMID:           200,
		TargetStorage:  "local-lvm",
		WorkingStorage: "local",
		Bridge:         "vmbr0",
		DiskFormat:     "qcow2",
		StartAfter:     true,
		LiveImport:     true,
	})

	if p.VMID != 200 {
		t.Errorf("VMID = %d, want 200", p.VMID)
	}
	if p.Name != "web01" {
		t.Errorf("Name = %q, want web01", p.Name)
	}
	if p.Cores != 4 || p.Sockets != 2 || p.Memory != 8192 {
		t.Errorf("cores/sockets/memory = %d/%d/%d, want 4/2/8192", p.Cores, p.Sockets, p.Memory)
	}
	if p.OSType != "l26" || p.BIOS != "ovmf" || p.ScsiHW != "virtio-scsi-pci" {
		t.Errorf("ostype/bios/scsihw = %q/%q/%q", p.OSType, p.BIOS, p.ScsiHW)
	}

	// Mapped create-args must not leak into Extra; un-mapped ones (smbios1) must.
	if _, ok := p.Extra["name"]; ok {
		t.Error("Extra should not contain mapped key 'name'")
	}
	if p.Extra["smbios1"] != "uuid=abc-123" {
		t.Errorf("Extra[smbios1] = %q, want uuid=abc-123", p.Extra["smbios1"])
	}

	want := map[string]string{
		"scsi0":                  "local-lvm:0,import-from=esxi:ha/ds/web01/web01.vmdk,format=qcow2",
		"scsi1":                  "local-lvm:0,import-from=esxi:ha/ds/web01/web01_1.vmdk,format=qcow2",
		"efidisk0":               "local-lvm:0,import-from=esxi:ha/ds/web01/web01.nvram,format=qcow2",
		"import-working-storage": "local",
		"live-restore":           "1",
	}
	for k, v := range want {
		if p.Extra[k] != v {
			t.Errorf("Extra[%s] = %q, want %q", k, p.Extra[k], v)
		}
	}
	if p.Net0 != "vmxnet3=AA:BB:CC:DD:EE:FF,bridge=vmbr0" {
		t.Errorf("Net0 = %q", p.Net0)
	}
	// Live import already boots the guest, so start and live-restore are mutually exclusive
	// — live import wins and an explicit start=1 must NOT also be emitted.
	if p.Start {
		t.Error("Start should be false when LiveImport is set (live import implies boot)")
	}
}

func TestBuildImportCreateParams_StartAfterWithoutLiveImport(t *testing.T) {
	meta := sampleMetadata()
	p := BuildImportCreateParams(meta, ImportCreateOptions{
		VMID:          200,
		TargetStorage: "local-lvm",
		StartAfter:    true,
	})
	if !p.Start {
		t.Error("Start should be true when StartAfter is set and LiveImport is not")
	}
	if _, ok := p.Extra["live-restore"]; ok {
		t.Error("live-restore should be absent when LiveImport is false")
	}
}

func TestBuildImportCreateParams_NoOptionalsNoFormat(t *testing.T) {
	meta := sampleMetadata()
	p := BuildImportCreateParams(meta, ImportCreateOptions{VMID: 1, TargetStorage: "ceph"})

	if p.Extra["scsi0"] != "ceph:0,import-from=esxi:ha/ds/web01/web01.vmdk" {
		t.Errorf("Extra[scsi0] = %q, want no format suffix", p.Extra["scsi0"])
	}
	if _, ok := p.Extra["import-working-storage"]; ok {
		t.Error("import-working-storage should be absent when WorkingStorage empty")
	}
	if _, ok := p.Extra["live-restore"]; ok {
		t.Error("live-restore should be absent when LiveImport false")
	}
	if p.Net0 != "" {
		t.Errorf("Net0 = %q, want empty when no bridge", p.Net0)
	}
	if p.Start {
		t.Error("Start should be false when StartAfter not set")
	}
}

func TestParsedDisks_ObjectAndStringForms(t *testing.T) {
	meta := sampleMetadata()
	disks := meta.ParsedDisks()
	if got := disks["scsi0"].Volid; got != "esxi:ha/ds/web01/web01.vmdk" {
		t.Errorf("scsi0 volid = %q", got)
	}
	if got := disks["scsi0"].ImageFormat; got != "vmdk" {
		t.Errorf("scsi0 image-format = %q, want vmdk", got)
	}
	if got := disks["scsi1"].Volid; got != "esxi:ha/ds/web01/web01_1.vmdk" {
		t.Errorf("scsi1 (bare string) volid = %q", got)
	}
}

func TestFlatCreateArgs_FlattensScalars(t *testing.T) {
	meta := &ImportMetadata{
		CreateArgs: map[string]json.RawMessage{
			"name":   json.RawMessage(`"web01"`),
			"cores":  json.RawMessage(`4`),
			"onboot": json.RawMessage(`true`),
		},
	}
	args := meta.FlatCreateArgs()
	if args["name"] != "web01" {
		t.Errorf("name = %q", args["name"])
	}
	if args["cores"] != "4" {
		t.Errorf("cores = %q, want 4", args["cores"])
	}
	if args["onboot"] != "1" {
		t.Errorf("onboot = %q, want 1", args["onboot"])
	}
}

func TestImportMetadataDecode_FromJSON(t *testing.T) {
	raw := `{
		"type": "vm",
		"source": "ova",
		"create-args": {"name": "appliance", "cores": 2, "memory": 4096},
		"disks": {"scsi0": {"volid": "local:import/app.ova/disk1.vmdk", "size": 1024}},
		"net": {"net0": {"model": "e1000"}},
		"warnings": [{"type": "ova-needs-extracting"}, {"type": "efi-state-lost"}]
	}`
	var meta ImportMetadata
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if meta.Source != "ova" {
		t.Errorf("source = %q", meta.Source)
	}
	if len(meta.Warnings) != 2 || meta.Warnings[0].Type != "ova-needs-extracting" {
		t.Errorf("warnings = %+v", meta.Warnings)
	}
	if d := meta.ParsedDisks()["scsi0"]; d.Volid != "local:import/app.ova/disk1.vmdk" {
		t.Errorf("disk volid = %q", d.Volid)
	}
	if meta.FlatCreateArgs()["memory"] != "4096" {
		t.Errorf("memory flatten = %q", meta.FlatCreateArgs()["memory"])
	}
}
