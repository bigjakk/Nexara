package auth

import "github.com/google/uuid"

// SystemUserID is the well-known UUID for the seeded system actor used to
// attribute audit-log entries and Proxmox tasks created by background
// processes (DRS, scheduler, rolling-update orchestrator, collector). The
// row is inserted by migration 000013_system_user, and is not a login
// account — registration / setup-status logic must exclude this UUID when
// counting "real" users.
var SystemUserID = uuid.MustParse("00000000-0000-0000-0000-000000000001")
