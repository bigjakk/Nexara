package migration

// Status constants for migration jobs.
const (
	StatusPending    = "pending"
	StatusChecking   = "checking"
	StatusMigrating  = "migrating"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
	StatusCancelled  = "cancelled"
)

// Migration type constants.
const (
	TypeIntraCluster = "intra-cluster"
	TypeCrossCluster = "cross-cluster"
)

// VM type constants.
const (
	VMTypeQEMU = "qemu"
	VMTypeLXC  = "lxc"
)

// Migration mode constants (for intra-cluster migrations).
const (
	ModeLive    = "live"    // Memory/live migration to a different node.
	ModeStorage = "storage" // Move all disks to a different storage (same node).
	ModeBoth    = "both"    // Live migration + storage migration.
)

// CheckSeverity represents the severity of a pre-flight check result.
type CheckSeverity string

const (
	SeverityPass CheckSeverity = "pass"
	SeverityWarn CheckSeverity = "warn"
	SeverityFail CheckSeverity = "fail"
)

// CheckResult holds the result of a single pre-flight check.
type CheckResult struct {
	Name     string        `json:"name"`
	Severity CheckSeverity `json:"severity"`
	Message  string        `json:"message"`
}

// PreFlightReport is the collection of all pre-flight check results.
type PreFlightReport struct {
	Checks  []CheckResult `json:"checks"`
	Passed  bool          `json:"passed"`
}

// StorageMapping maps source storage names to target storage names.
type StorageMapping map[string]string

// NetworkMapping maps source bridge names to target bridge names.
type NetworkMapping map[string]string
