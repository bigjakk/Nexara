package proxmox

import "strings"

// TaskSucceeded reports whether a Proxmox task's `exitstatus` indicates a
// successful completion. Proxmox returns "OK" for clean exits but also
// emits "OK (with warnings)" and "WARNINGS: N" (e.g. "WARNINGS: 2") when the
// task completed with non-fatal warnings (the task itself succeeded). An empty
// exit status on a stopped task also indicates success on some Proxmox versions.
//
// This is the single source of truth for the success rule. It is consumed by
// the DRS executor, the rolling-update and migration orchestrators, AND the
// collector reconciler/ingester (via classifyTaskExit), so every finalizer that
// races to mark the same UPID's task_history row agrees on the terminal state.
// The frontend mirror lives in task-status.ts (isOkExit).
func TaskSucceeded(exitStatus string) bool {
	upper := strings.ToUpper(strings.TrimSpace(exitStatus))
	return upper == "" || upper == "OK" || strings.HasPrefix(upper, "OK ") || strings.HasPrefix(upper, "WARNINGS")
}
