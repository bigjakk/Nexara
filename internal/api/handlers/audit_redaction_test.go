package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// grantEngine satisfies permissionEngine from an explicit set of
// "action:resource" grants, so a caller can hold view:audit without holding
// manage:audit. That distinction is the whole subject of these tests and the
// shared admin/viewer stub cannot express it — it answers every permission the
// same way.
type grantEngine struct{ granted map[string]bool }

func newGrantEngine(perms ...string) *grantEngine {
	granted := make(map[string]bool, len(perms))
	for _, p := range perms {
		granted[p] = true
	}
	return &grantEngine{granted: granted}
}

func (e *grantEngine) HasPermission(_ context.Context, _ uuid.UUID, action, resource, _ string, _ uuid.UUID) (bool, error) {
	return e.granted[action+":"+resource], nil
}

func (e *grantEngine) HasGlobalPermission(_ context.Context, _ uuid.UUID, action, resource string) (bool, error) {
	return e.granted[action+":"+resource], nil
}

func (e *grantEngine) LoadUserPermissions(_ context.Context, _ uuid.UUID) (*auth.UserPermissions, error) {
	perms := &auth.UserPermissions{}
	for p := range e.granted {
		action, resource, _ := strings.Cut(p, ":")
		perms.Permissions = append(perms.Permissions, auth.ScopedPermission{
			Action: action, Resource: resource, ScopeType: "global",
		})
	}
	return perms, nil
}

// structRows replays any sqlc row struct through pgx.Rows by assigning its
// fields positionally, which is the order the generated Scan passes its
// destinations in. Generic rather than per-row-type so a read path can be
// exercised without hand-writing a fake for its particular projection; it
// reports a mismatch rather than silently zero-filling if that order changes.
type structRows struct {
	rows []any
	i    int
	err  error
}

func (r *structRows) Next() bool {
	if r.i >= len(r.rows) {
		return false
	}
	r.i++
	return true
}

func (r *structRows) Scan(dest ...any) error {
	row := reflect.ValueOf(r.rows[r.i-1])
	if row.NumField() != len(dest) {
		r.err = fmt.Errorf("scan got %d destinations, want %d — the select column list changed",
			len(dest), row.NumField())
		return r.err
	}
	for i, d := range dest {
		ptr := reflect.ValueOf(d)
		if ptr.Kind() != reflect.Pointer {
			r.err = fmt.Errorf("scan destination %d is %T, not a pointer", i, d)
			return r.err
		}
		if ptr.Elem().Type() != row.Field(i).Type() {
			r.err = fmt.Errorf("scan destination %d is *%s, want *%s — the select column order changed",
				i, ptr.Elem().Type(), row.Field(i).Type())
			return r.err
		}
		ptr.Elem().Set(row.Field(i))
	}
	return nil
}

func (r *structRows) Err() error                                 { return r.err }
func (*structRows) Close()                                       {}
func (*structRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (*structRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (*structRows) Values() ([]any, error)                       { return nil, errCaptured }
func (*structRows) RawValues() [][]byte                          { return nil }
func (*structRows) Conn() *pgx.Conn                              { return nil }

// auditRowsDBTX replays a fixed result set to any Query. The read-side
// counterpart to captureDBTX, which records arguments but cannot hand a handler
// rows to filter.
type auditRowsDBTX struct{ rows []any }

func (*auditRowsDBTX) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errCaptured
}

func (d *auditRowsDBTX) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return &structRows{rows: d.rows}, nil
}

func (*auditRowsDBTX) QueryRow(context.Context, string, ...any) pgx.Row {
	return failRow{err: errCaptured}
}

// recentAuditRow builds one row of the dashboard activity feed. Only the three
// fields the redaction reads matter; the rest carry plausible values so the
// response marshals like a real one.
func recentAuditRow(resourceType, resourceID, action, details string) db.ListRecentAuditLogEnrichedRow {
	return db.ListRecentAuditLogEnrichedRow{
		ID:           uuid.New(),
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Action:       action,
		Details:      json.RawMessage(details),
		CreatedAt:    time.Unix(0, 0).UTC(),
		Source:       "api",
	}
}

// TestAuditDetailsForRedactsOwnedSettings covers the rule itself: which rows
// have their details withheld, and from whom.
//
// The audit entries for a reserved setting key carry that key's value — the
// syslog forwarding entries name the collector the audit stream is sent to.
// view:audit belongs to the default Viewer role (migrations/000016_rbac), while
// the endpoint that owns the key wants manage:audit, granted to Admin and
// Operator only (migrations/000040). Serving those details to every view:audit
// holder would hand a Viewer the value GET /api/v1/settings/<key> refuses them,
// undoing half of the reservation in settings.go.
func TestAuditDetailsForRedactsOwnedSettings(t *testing.T) {
	const details = `{"new":{"host":"siem-a.internal"}}`

	tests := []struct {
		name         string
		resourceType string
		resourceID   string
		visible      map[string]bool
		wantRedacted bool
	}{
		{
			name:         "owned key is withheld without the owning permission",
			resourceType: settingResourceType, resourceID: syslogSettingKey,
			visible:      map[string]bool{syslogSettingKey: false},
			wantRedacted: true,
		},
		{
			name:         "owned key is served to a caller who holds it",
			resourceType: settingResourceType, resourceID: syslogSettingKey,
			visible:      map[string]bool{syslogSettingKey: true},
			wantRedacted: false,
		},
		{
			// The reservation is per key, not per resource type: branding is
			// world-readable by design and its entries stay legible.
			name:         "an unreserved setting key is untouched",
			resourceType: settingResourceType, resourceID: "branding.app_title",
			visible:      map[string]bool{syslogSettingKey: false},
			wantRedacted: false,
		},
		{
			// A VM that happens to be named after the key is not the setting.
			name:         "another resource type with a colliding id is untouched",
			resourceType: "vm", resourceID: syslogSettingKey,
			visible:      map[string]bool{syslogSettingKey: false},
			wantRedacted: false,
		},
		{
			// A missing entry reads as false, so a caller whose permissions
			// could not be resolved sees less, not more.
			name:         "an unresolved permission withholds",
			resourceType: settingResourceType, resourceID: syslogSettingKey,
			visible:      nil,
			wantRedacted: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := auditDetailsFor(tt.resourceType, tt.resourceID, details, tt.visible)

			if !tt.wantRedacted {
				if got != details {
					t.Errorf("details = %s, want them served verbatim (%s)", got, details)
				}
				return
			}
			if strings.Contains(got, "siem-a.internal") {
				t.Errorf("details = %s, want the value withheld", got)
			}
			if !json.Valid([]byte(got)) {
				t.Errorf("redacted details %q are not valid JSON — the client cannot parse the row", got)
			}
			owner := reservedGlobalSettings[syslogSettingKey]
			if want := owner.Action + ":" + owner.Resource; !strings.Contains(got, want) {
				t.Errorf("details = %s, want it to name the permission %q so a reader knows what to ask for",
					got, want)
			}
		})
	}
}

// TestListRecentRedactsForCallersWithoutTheOwningPermission drives the rule
// through a real handler, so the wiring is covered and not just the helper.
// ListRecent is the dashboard's activity feed — the widest-reaching of the
// audit reads, and the one a Viewer sees on login.
func TestListRecentRedactsForCallersWithoutTheOwningPermission(t *testing.T) {
	const collector = "siem-a.internal"
	syslogDetails := `{"previous":{"host":"` + collector + `","port":6514},"new":{"enabled":false}}`

	dbtx := &auditRowsDBTX{rows: []any{
		recentAuditRow(settingResourceType, syslogSettingKey, syslogUpdatedAction, syslogDetails),
		recentAuditRow(settingResourceType, "branding.app_title", "setting_updated",
			`{"key":"branding.app_title","scope":"global"}`),
	}}

	tests := []struct {
		name      string
		perms     []string
		wantValue bool
	}{
		{"a Viewer holds view:audit only", []string{"view:audit"}, false},
		{"an Operator also holds manage:audit", []string{"view:audit", "manage:audit"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewAuditHandler(db.New(dbtx), nil)
			app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
			app.Use(func(c fiber.Ctx) error {
				c.Locals("user_id", testSettingsUserID)
				c.Locals("rbac_engine", newGrantEngine(tt.perms...))
				return c.Next()
			})
			app.Get("/audit-log/recent", handler.ListRecent)

			resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/audit-log/recent", nil))
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want %d, body: %s", resp.StatusCode, http.StatusOK, body)
			}

			var got ListResponse[auditLogResponse]
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decode body %s: %v", body, err)
			}
			// Both rows must survive the cluster filter — otherwise "no
			// collector in the body" would pass for the wrong reason.
			if len(got.Items) != 2 {
				t.Fatalf("got %d rows, want 2 — the entries were filtered out, not redacted: %s", len(got.Items), body)
			}

			if strings.Contains(string(body), collector) != tt.wantValue {
				t.Errorf("collector host present = %v, want %v: %s",
					strings.Contains(string(body), collector), tt.wantValue, body)
			}
			// The entry itself always survives: who changed the forwarding
			// config, and when, is not the part that is owned.
			if got.Items[0].Action != syslogUpdatedAction || got.Items[0].ResourceID != syslogSettingKey {
				t.Errorf("the syslog entry lost its identity: %+v", got.Items[0])
			}
			if !strings.Contains(got.Items[1].Details, "branding.app_title") {
				t.Errorf("an unreserved setting's details were redacted too: %s", got.Items[1].Details)
			}
		})
	}
}

// TestGuard_AuditReadPathsRedact is the drift guard behind the rule: every
// function in audit.go that reads a row's Details must put it through
// auditDetailsFor.
//
// There are four such functions today — two response converters and two
// exporters — and a fifth is one new output format away. The disclosure is
// silent, so a path that forgets produces no error, no failing assertion, and no
// symptom until someone notices a Viewer can read the SIEM address.
func TestGuard_AuditReadPathsRedact(t *testing.T) {
	fset, files := parseGoFiles(t, ".")

	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}

			var readsDetails, redacts bool
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.SelectorExpr:
					// A row's Details field. Composite-literal keys are plain
					// idents, so setting Details: on a response is not a read.
					if node.Sel.Name == "Details" {
						readsDetails = true
					}
				case *ast.CallExpr:
					if callName(node) == "auditDetailsFor" {
						redacts = true
					}
				}
				return true
			})
			if !readsDetails || redacts {
				continue
			}

			pos := fset.Position(fn.Pos())
			t.Errorf("%s: %q reads an audit row's Details but never calls auditDetailsFor.\n"+
				"\tEntries about a reserved setting key carry that key's value, and view:audit is held "+
				"by the default Viewer role. Route the field through auditDetailsFor with the "+
				"visibility from reservedSettingVisibility.", pos, fn.Name.Name)
		}
	}
}
