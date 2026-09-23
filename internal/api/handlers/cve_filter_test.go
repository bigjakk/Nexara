package handlers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// cveFilterDBTX answers GetCVEScan with its one scan and every vulnerability
// listing with no rows, and records which listing ran and with what.
type cveFilterDBTX struct {
	scan  db.CveScan
	query string
	args  []any
}

func (*cveFilterDBTX) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errCaptured
}

func (d *cveFilterDBTX) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	for _, name := range []string{"ListCVEScanVulns", "ListCVEScanVulnsKEV", "ListCVEScanVulnsByNode", "ListCVEScanVulnsBySeverity"} {
		if strings.Contains(sql, "-- name: "+name+" :many") {
			d.query, d.args = name, args
			return &structRows{}, nil
		}
	}
	return nil, errCaptured
}

func (d *cveFilterDBTX) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	if !strings.Contains(sql, "-- name: GetCVEScan :one") {
		return failRow{err: errCaptured}
	}
	return replayRow{row: d.scan}
}

// TestCVEVulnerabilityListTreatsAnEmptyFilterAsNone drives the real
// ListVulnerabilities handler with ?severity=, ?node_id= and ?kev= sent EMPTY
// — the "no filter" each meant before the registry (`severity != ""`,
// `nodeID != ""`, and `c.Query("kev") == "true"`, which made ?kev= false) —
// and checks each reaches the listing an omitted one does, instead of a
// severity match on "", a uuid parse of "" or a refused boolean. An empty kev
// reaches the handler as NOT supplied, since its declaration counts "" as
// absent. The non-empty filters are the control that shows each is read at
// all — the node in either case of hex digit, since the rule admits upper
// case, which the uuid format used to lower-case — and the mixed cases pin
// that an empty filter never shadows a real one, and the order the handler
// applies real ones in, which the severity Description states: kev, then
// node_id, then severity, one at a time.
//
// No permission engine is wired because this route has no permission side in
// the handler: its gate is the declared middleware, and the only check here is
// that the scan belongs to the cluster in the path.
func TestCVEVulnerabilityListTreatsAnEmptyFilterAsNone(t *testing.T) {
	clusterID, scanID, nodeID := uuid.New(), uuid.New(), uuid.New()
	mirror := compiledMirror(t, apischema.Properties{
		"cluster_id": {Type: apischema.String, Format: "uuid", Source: apischema.SourcePath},
		"scan_id":    {Type: apischema.String, Format: "uuid", Source: apischema.SourcePath},
		"severity": {Type: apischema.String, Optional: true,
			Enum: append([]string{""}, CVESeverities...)},
		"node_id": {Type: apischema.String, Optional: true,
			Pattern: apischema.Rule("uuid-or-empty"), MaxLength: apischema.Ptr(36)},
		"kev": {Type: apischema.Boolean, Optional: true, Default: false, EmptyIsAbsent: true},
	})

	cases := []struct {
		name  string
		query string
		// empties are the filters the query sends as "", which must reach
		// the handler as supplied — or an "empty" case would quietly be the
		// omitted one. kev is not among them: an empty kev must arrive NOT
		// supplied, which unsent checks.
		empties []string
		unsent  []string
		want    string
		args    []any
	}{
		{"omitted", "", nil, nil, "ListCVEScanVulns", []any{scanID}},
		{"empty", "?severity=&node_id=", []string{"severity", "node_id"}, nil, "ListCVEScanVulns", []any{scanID}},
		{"an empty kev", "?kev=", nil, []string{"kev"}, "ListCVEScanVulns", []any{scanID}},
		{"all empty", "?severity=&node_id=&kev=", []string{"severity", "node_id"}, []string{"kev"},
			"ListCVEScanVulns", []any{scanID}},
		{"kev false", "?kev=false", nil, nil, "ListCVEScanVulns", []any{scanID}},
		{"a severity", "?severity=high", nil, nil, "ListCVEScanVulnsBySeverity", []any{scanID, "high"}},
		{"a node", "?node_id=" + nodeID.String(), nil, nil, "ListCVEScanVulnsByNode", []any{scanID, nodeID}},
		{"kev", "?kev=true", nil, nil, "ListCVEScanVulnsKEV", []any{scanID}},
		{"a node in upper case", "?node_id=" + strings.ToUpper(nodeID.String()), nil, nil,
			"ListCVEScanVulnsByNode", []any{scanID, nodeID}},
		{"a severity beside an empty node", "?severity=high&node_id=", []string{"node_id"}, nil,
			"ListCVEScanVulnsBySeverity", []any{scanID, "high"}},
		{"a node beside an empty severity", "?severity=&node_id=" + nodeID.String(), []string{"severity"}, nil,
			"ListCVEScanVulnsByNode", []any{scanID, nodeID}},
		{"a severity beside an empty kev", "?kev=&severity=high", nil, []string{"kev"},
			"ListCVEScanVulnsBySeverity", []any{scanID, "high"}},
		{"a node beside an empty kev", "?kev=&node_id=" + nodeID.String(), nil, []string{"kev"},
			"ListCVEScanVulnsByNode", []any{scanID, nodeID}},
		// The order real filters are applied in: one at a time, kev first,
		// then the node, then the severity.
		{"a node beside a severity", "?severity=high&node_id=" + nodeID.String(), nil, nil,
			"ListCVEScanVulnsByNode", []any{scanID, nodeID}},
		{"kev beside a node and a severity", "?kev=true&severity=high&node_id=" + nodeID.String(), nil, nil,
			"ListCVEScanVulnsKEV", []any{scanID}},
	}

	// Every filter the schema declares is sent EMPTY by some case, so none
	// can be added to it without being tested that way.
	var sentEmpty []string
	for _, tt := range cases {
		sentEmpty = append(sentEmpty, tt.empties...)
		sentEmpty = append(sentEmpty, tt.unsent...)
	}
	assertFiltersCoverSchema(t, mirror, slices.Compact(slices.Sorted(slices.Values(sentEmpty))), "cluster_id", "scan_id")

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			dbtx := &cveFilterDBTX{scan: db.CveScan{ID: scanID, ClusterID: clusterID}}
			handler := NewCVEHandler(nil, db.New(dbtx), "", nil, nil, nil)
			var seen *apischema.Params
			app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
			app.Get("/clusters/:cluster_id/cve-scans/:scan_id/vulnerabilities",
				withRequestParams(t, mirror, []string{"cluster_id", "scan_id"}, func(c fiber.Ctx, p *apischema.Params) error {
					seen = p
					return handler.ListVulnerabilities(c, p)
				}))

			target := "/clusters/" + clusterID.String() + "/cve-scans/" + scanID.String() + "/vulnerabilities" + tt.query
			resp, err := app.Test(httptest.NewRequest(http.MethodGet, target, nil))
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, _ := io.ReadAll(resp.Body)
			arrivedEmpty(t, listReply{status: resp.StatusCode, body: string(body), params: seen}, tt.empties...)
			for _, name := range tt.unsent {
				if seen.Has(name) || seen.Bool(name) {
					t.Fatalf("precondition: %s reached the handler as (%v, supplied=%v), want its default "+
						"(false, not supplied)", name, seen.Bool(name), seen.Has(name))
				}
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body: %s)", resp.StatusCode, body)
			}
			if dbtx.query != tt.want {
				t.Errorf("ran %q, want %q", dbtx.query, tt.want)
			}
			if !reflect.DeepEqual(dbtx.args, tt.args) {
				t.Errorf("%s was handed %v, want %v", dbtx.query, dbtx.args, tt.args)
			}
		})
	}
}
