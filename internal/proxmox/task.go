package proxmox

import "strings"

// TaskSucceeded reports whether a Proxmox task's `exitstatus` indicates a
// successful completion. Proxmox returns "OK" for clean exits but also
// emits "OK (with warnings)" / "WARNINGS" when the task completed with
// non-fatal warnings (the task itself succeeded). An empty exit status
// on a stopped task also indicates success on some Proxmox versions.
//
// This was previously duplicated as `taskSucceeded` in
// internal/drs/executor.go and internal/rolling/orchestrator.go, and as
// `migrationSucceeded` in internal/migration/orchestrator.go — three
// byte-identical bodies, consolidated here in Phase 5.3.
func TaskSucceeded(exitStatus string) bool {
	upper := strings.ToUpper(strings.TrimSpace(exitStatus))
	return upper == "" || upper == "OK" || strings.HasPrefix(upper, "OK ") || upper == "WARNINGS"
}
