// Package migrations exposes the canonical SQL migration files as an embedded
// filesystem so the same files drive both `make migrate-up` (CLI tool reading
// the directory) and Docker startup (binary reading the embedded FS).
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
