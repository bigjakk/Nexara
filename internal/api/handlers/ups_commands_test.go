package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/nut"
	"github.com/bigjakk/nexara/internal/nut/nuttest"
	"github.com/bigjakk/nexara/internal/upscontrol"
)

// The command routes put the tier rules, the stored account and the run path
// together: cutting the UPS's power, or setting a variable that can change it,
// needs execute:ups and manage:node on every cluster it powers (everywhere when
// none is known), and the typed name where its confirm says so; a refusal
// records and sends nothing; a run sent is audited and watched to the driver's
// answer.
func TestUpsCommandRoutes(t *testing.T) {
	t.Parallel()
	fake := nuttest.NewServer(t)
	fake.SetDevice("ups01", nuttest.Device{
		Vars: map[string]string{"ups.status": "OL", "battery.charge.low": "10", "device.location": "",
			"driver.flag.allow_killpower": "0"},
		Types: map[string]string{"battery.charge.low": "RW STRING:10", "device.location": "RW STRING:32",
			"driver.flag.allow_killpower": "RW NUMBER"},
		Commands: []string{"beeper.mute", "load.off", "shutdown.stop"},
	})
	fake.SetUser("nexara", nuttest.User{Password: "pw", SET: true, Instcmds: []string{"ALL"}})
	fake.SetPendingReads(1) // so the watch asks more than once
	host, port := fake.Addr()
	password, err := crypto.Encrypt("pw", testEncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	server := db.NutServer{ID: uuid.New(), Name: "nut01", Host: host, Port: int32(port), TlsMode: "off",
		Username: "nexara", PasswordEncrypted: password}
	commands, _ := json.Marshal([]upscontrol.Command{{Name: "beeper.mute"}, {Name: "load.off"}, {Name: "shutdown.stop"}})
	writable, _ := json.Marshal(map[string]upscontrol.Var{
		"battery.charge.low": {Type: upscontrol.TypeString, MaxLen: 10}, "device.location": {Type: upscontrol.TypeString, MaxLen: 32},
		"driver.flag.allow_killpower": {Type: upscontrol.TypeNumber},
	})
	dev := db.GetUpsDeviceRow{ID: uuid.New(), ServerID: server.ID, UpsName: "ups01", ServerName: "nut01",
		Vars: json.RawMessage(`{"battery.charge.low":"10"}`), Commands: commands, RwVars: writable,
		CommandsListedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}}
	fed := uuid.New()
	everywhere := newGrantEngine("execute:ups", "manage:ups", "manage:node")
	// execute:ups and manage:ups everywhere, manage:node on the cluster it powers only.
	scoped := upsCaller{clusterGrantEngine{"manage", "node", fed}, []string{"execute:ups", "manage:ups"}}
	str := apischema.Property{Type: apischema.String, Optional: true}
	for _, tc := range []struct {
		name, method, target string
		body                 map[string]any
		engine               permissionEngine
		nothingFed           bool
		full                 bool // every watch already taken
		want                 int
		sent, outcome        string // the line upsd received and the status the run ends in, for a run
	}{
		{"cuts power, not confirmed", http.MethodPost, "/commands", map[string]any{"command": "load.off"}, everywhere, false, false, fiber.StatusBadRequest, "", ""},
		{"cuts power, with a cluster it powers out of reach", http.MethodPost, "/commands", map[string]any{"command": "load.off", "confirm": "ups01"},
			upsCaller{clusterGrantEngine{"manage", "node", uuid.New()}, []string{"execute:ups"}}, false, false, fiber.StatusForbidden, "", ""},
		{"cuts power on a UPS that powers nothing known", http.MethodPost, "/commands", map[string]any{"command": "load.off", "confirm": "ups01"},
			scoped, true, false, fiber.StatusForbidden, "", ""},
		{"cuts power, confirmed", http.MethodPost, "/commands", map[string]any{"command": "load.off", "confirm": "ups01"}, scoped, false, false,
			fiber.StatusAccepted, "INSTCMD ups01 load.off", "succeeded"},
		{"cuts power, every watch taken", http.MethodPost, "/commands", map[string]any{"command": "load.off", "confirm": "ups01"}, everywhere, false, true,
			fiber.StatusTooManyRequests, "", ""},
		{"a stop, every watch taken", http.MethodPost, "/commands", map[string]any{"command": "shutdown.stop"}, everywhere, false, true,
			fiber.StatusAccepted, "INSTCMD ups01 shutdown.stop", "unconfirmed"},
		{"a value past STRING:10", http.MethodPut, "/vars/battery.charge.low", map[string]any{"value": "01234567890"}, everywhere, false, false, fiber.StatusBadRequest, "", ""},
		{"a power setting without execute:ups", http.MethodPut, "/vars/battery.charge.low", map[string]any{"value": "20"},
			newGrantEngine("manage:ups", "manage:node"), false, false, fiber.StatusForbidden, "", ""},
		{"the killpower flag, not confirmed", http.MethodPut, "/vars/driver.flag.allow_killpower", map[string]any{"value": "1"}, everywhere, false, false, fiber.StatusBadRequest, "", ""},
		{"a power setting", http.MethodPut, "/vars/battery.charge.low", map[string]any{"value": "20"}, scoped, false, false,
			fiber.StatusAccepted, "SET VAR ups01 battery.charge.low 20", "succeeded"},
		{"a harmless setting with manage:ups alone", http.MethodPut, "/vars/device.location", map[string]any{"value": "rack2"},
			newGrantEngine("manage:ups"), false, false, fiber.StatusAccepted, "SET VAR ups01 device.location rack2", "succeeded"},
	} {
		cmd, _ := tc.body["command"].(string)
		run := db.UpsCommand{ID: uuid.New(), UpsID: dev.ID, Kind: "command", Name: cmd, Status: "sent"}
		if tc.method == http.MethodPut {
			run.Kind, run.Name, run.Argument = "set", strings.TrimPrefix(tc.target, "/vars/"), tc.body["value"].(string)
		}
		fedRows := []any{struct{ ID uuid.UUID }{fed}}
		if tc.nothingFed {
			fedRows = nil
		}
		dbtx := &auditRowsDBTX{
			rowFor:  map[string]any{"GetUpsDevice": dev, "GetNutServer": server, "InsertUpsCommand": run},
			rowsFor: map[string][]any{"ListUpsFedClusters": fedRows},
			execOK:  map[string]bool{"UpdateUpsCommand": true, "SetNutServerCommandsVerified": true, "InsertAuditLog": true},
		}
		h := NewNutHandler(db.New(dbtx), testEncryptionKey, nil)
		h.trackingPoll = time.Millisecond
		if tc.full {
			for range cap(h.runSlots) {
				h.runSlots <- struct{}{}
			}
		}
		app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
		app.Use(func(c fiber.Ctx) error {
			c.Locals("user_id", uuid.New())
			c.Locals("rbac_engine", tc.engine)
			return c.Next()
		})
		app.Post("/ups/:id/commands", withRequestParams(t, apischema.Properties{"id": str, "command": str, "parameter": str, "confirm": str}, []string{"id"}, h.RunCommand))
		app.Put("/ups/:id/vars/:name", withRequestParams(t, apischema.Properties{"id": str, "name": str, "value": str, "confirm": str}, []string{"id", "name"}, h.SetVariable))
		before := len(fake.Received())
		body, _ := json.Marshal(tc.body)
		req := httptest.NewRequest(tc.method, "/ups/"+dev.ID.String()+tc.target, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("%s: status %d, want %d", tc.name, resp.StatusCode, tc.want)
			continue
		}
		// Every run slot taken back means every watch has recorded its outcome
		// (with every watch taken up front, the run was not watched).
		for range cap(h.runSlots) {
			if tc.full {
				break
			}
			select {
			case h.runSlots <- struct{}{}:
			case <-time.After(10 * time.Second):
				t.Fatalf("%s: a run's watch did not end", tc.name)
			}
		}
		changes := slices.DeleteFunc(fake.Received()[before:], func(l string) bool {
			return !strings.HasPrefix(l, "INSTCMD ") && !strings.HasPrefix(l, "SET VAR ")
		})
		if tc.sent == "" {
			if len(changes) != 0 || dbtx.queriedRow("InsertUpsCommand") {
				t.Errorf("%s: refused, yet a run was recorded or upsd received %q", tc.name, changes)
			}
			continue
		}
		updates := dbtx.execsOf("UpdateUpsCommand")
		audits := dbtx.execsOf("InsertAuditLog")
		if !slices.Contains(changes, tc.sent) || len(updates) == 0 || updates[len(updates)-1][0] != tc.outcome {
			t.Errorf("%s: upsd received %q, run updates %v; want %q ending %s", tc.name, changes, updates, tc.sent, tc.outcome)
		}
		if len(audits) != 1 || audits[0][4] != "ups_command_sent" {
			t.Errorf("%s: audit rows %v; want one ups_command_sent", tc.name, audits)
		}
		// Only a command upsd took proves the account may run commands.
		if verified := len(dbtx.execsOf("SetNutServerCommandsVerified")) == 1; verified != (tc.method == http.MethodPost) {
			t.Errorf("%s: account marked verified = %v", tc.name, verified)
		}
	}
}

func TestTrackedOutcome(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		got            nut.TrackingStatus
		kind, status   string
		resultContains string
	}{
		{nut.TrackingSuccess, "command", "succeeded", ""},
		{nut.TrackingFailed, "command", "failed", "reported that it failed"},
		{nut.TrackingInvalid, "set", "failed", "refused the value"},
		{nut.TrackingInvalid, "command", "failed", "not valid now"},
		{nut.TrackingUnknown, "command", "failed", "does not carry it out"},
		{nut.TrackingPending, "command", "unconfirmed", "had not answered"},
	} {
		status, result := trackedOutcome(tc.got, tc.kind)
		if status != tc.status || !strings.Contains(result, tc.resultContains) {
			t.Errorf("trackedOutcome(%s, %s) = %q, %q; want %q containing %q", tc.got, tc.kind, status, result, tc.status, tc.resultContains)
		}
	}
}
