package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
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
)

// newNutTestApp mounts probe, create and update on h. With a nil queries
// handle a case that reached the store would panic rather than pass: probe
// and create run validation, the two confirm gates and the probe first.
func newNutTestApp(t *testing.T, h *NutHandler) *fiber.App {
	t.Helper()
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	props := apischema.Properties{
		"name":                              {Type: apischema.String, Optional: true},
		"host":                              {Type: apischema.String},
		"port":                              {Type: apischema.Integer, Optional: true, Default: 3493},
		"username":                          {Type: apischema.String, Optional: true},
		"password":                          {Type: apischema.String, Optional: true},
		"tls_mode":                          {Type: apischema.String, Optional: true, Default: "off"},
		"verify_tls":                        {Type: apischema.Boolean, Optional: true, Default: true},
		"tls_fingerprint":                   {Type: apischema.String, Optional: true},
		"allow_private_address":             {Type: apischema.Boolean, Optional: true},
		"acknowledge_cleartext_credentials": {Type: apischema.Boolean, Optional: true},
		"acknowledge_insecure_tls":          {Type: apischema.Boolean, Optional: true},
	}
	app.Post("/nut-servers/probe", veeamHandlerWithParams(t, props, nil, h.Probe))
	app.Post("/nut-servers", veeamHandlerWithParams(t, props, nil, h.Create))
	update := maps.Clone(props)
	update["id"] = apischema.Property{Type: apischema.String, Format: "uuid", Source: apischema.SourcePath}
	update["host"] = apischema.Property{Type: apischema.String, Optional: true}
	update["enabled"] = apischema.Property{Type: apischema.Boolean, Optional: true}
	update["clear_credentials"] = apischema.Property{Type: apischema.Boolean, Optional: true}
	app.Put("/nut-servers/:id", veeamHandlerWithParams(t, update, []string{"id"}, h.Update))
	return app
}

func TestNutProbeAndCreate_RefuseOrConfirmBeforeStoring(t *testing.T) {
	t.Parallel()
	fake := nuttest.NewServer(t)
	fake.SetDevice("ups01", nuttest.Device{Description: "Rack UPS"})
	host, port := fake.Addr()
	denied := nuttest.NewServer(t)
	denied.Fail("LIST UPS", "ACCESS-DENIED")
	_, deniedPort := denied.Addr()
	app := newNutTestApp(t, NewNutHandler(nil, testEncryptionKey, nil))

	for _, tc := range []struct {
		name, path, body string
		status           int
		want             string
	}{
		{"scheme in host", "/nut-servers/probe", `{"host":"tcp://192.0.2.40"}`, http.StatusBadRequest, "without a scheme or port"},
		{"port in host", "/nut-servers/probe", `{"host":"192.0.2.40:3493"}`, http.StatusBadRequest, "without a scheme or port"},
		{"password without username", "/nut-servers/probe", `{"host":"192.0.2.40","password":"pw"}`, http.StatusBadRequest, "needs a username"},
		{"private address", "/nut-servers/probe", `{"host":"10.20.30.40"}`, http.StatusUnprocessableEntity, "private_address_confirm_required"},
		{"password in clear", "/nut-servers/probe",
			fmt.Sprintf(`{"host":%q,"port":%d,"username":"u","password":"pw","allow_private_address":true}`, host, port),
			http.StatusUnprocessableEntity, "nut_cleartext_credentials_confirm_required"},
		{"unverified TLS with a password", "/nut-servers/probe",
			fmt.Sprintf(`{"host":%q,"port":%d,"username":"u","password":"pw","tls_mode":"starttls","verify_tls":false,"allow_private_address":true}`, host, port),
			http.StatusUnprocessableEntity, "nut_insecure_tls_confirm_required"},
		{"nothing listening", "/nut-servers", `{"name":"nut01","host":"127.0.0.1","port":1,"allow_private_address":true}`,
			http.StatusBadGateway, "nut_unreachable"},
		{"refused by upsd", "/nut-servers", fmt.Sprintf(`{"name":"nut01","host":"127.0.0.1","port":%d,"allow_private_address":true}`, deniedPort),
			http.StatusUnprocessableEntity, "nut_auth_failed"},
		{"TLS not offered", "/nut-servers/probe", fmt.Sprintf(`{"host":%q,"port":%d,"tls_mode":"starttls","allow_private_address":true}`, host, port),
			http.StatusUnprocessableEntity, "nut_tls_unavailable"},
	} {
		status, body := doVeeamRequest(t, app, http.MethodPost, tc.path, "", tc.body)
		if status != tc.status || !strings.Contains(body, tc.want) {
			t.Errorf("%s: %d %s, want %d containing %q", tc.name, status, body, tc.status, tc.want)
		}
	}

	// And a probe that gets through reports what the server serves.
	status, body := doVeeamRequest(t, app, http.MethodPost, "/nut-servers/probe", "",
		fmt.Sprintf(`{"host":%q,"port":%d,"allow_private_address":true}`, host, port))
	var probe nutProbeResponse
	if status != http.StatusOK || json.Unmarshal([]byte(body), &probe) != nil ||
		probe.ProtocolVersion != "1.3" || len(probe.Devices) != 1 || probe.Devices[0].Name != "ups01" {
		t.Errorf("probe: %d %s", status, body)
	}
}

// An edit asks only when it weakens how the stored password travels,
// whichever confirm the server was saved with.
func TestNutUpdateTransportAck(t *testing.T) {
	t.Parallel()
	off := nutTarget{password: "set", tlsMode: "off"}
	unverified := nutTarget{password: "set", tlsMode: "starttls"}
	pinned := nutTarget{password: "set", tlsMode: "starttls", fingerprint: "AA:BB:CC"}
	for _, tc := range []struct {
		name      string
		was, next nutTarget
		ack       bool
		want      string
	}{
		{"unverified TLS to plain text", unverified, off, false, "nut_cleartext_credentials_confirm_required"},
		{"the same, confirmed", unverified, off, true, ""},
		{"plain text, transport unchanged", off, off, false, ""},
		{"plain text to unverified TLS is a step up", off, unverified, false, ""},
		{"a password added to a plain-text server", nutTarget{tlsMode: "off"}, off, false, "nut_cleartext_credentials_confirm_required"},
		{"pin removed with verification off", pinned, unverified, false, "nut_insecure_tls_confirm_required"},
	} {
		got := ""
		var cr *confirmRequiredError
		if err := nutUpdateTransportAck(tc.was, tc.next, tc.ack, tc.ack); errors.As(err, &cr) {
			got = cr.Code
		} else if err != nil {
			got = err.Error()
		}
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Update refuses before it dials: a move that would carry the stored password
// to a new address, and a move to plain text this server was never confirmed
// for (it was saved with unverified TLS). A rename saves without dialing.
func TestNutUpdate_RefusesBeforeDialing(t *testing.T) {
	t.Parallel()
	pw, err := crypto.Encrypt("pw", testEncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	stored := db.NutServer{ID: uuid.New(), Name: "nut01", Host: "192.0.2.40", Port: 3493, Username: "u",
		PasswordEncrypted: pw, TlsMode: "starttls", Enabled: true}
	h := NewNutHandler(db.New(&auditRowsDBTX{rowFor: map[string]any{"GetNutServer": stored, "UpdateNutServer": stored}}),
		testEncryptionKey, nil)
	var dials atomic.Int32
	h.dial = func(context.Context, nut.Config) (*nut.Client, error) {
		dials.Add(1)
		return nil, errors.New("dialed")
	}
	app := newNutTestApp(t, h)

	for _, tc := range []struct {
		name, body string
		status     int
		want       string
	}{
		{"host moved", `{"host":"192.0.2.41","allow_private_address":true}`, http.StatusBadRequest, "re-entering the password"},
		{"port moved", `{"port":3494}`, http.StatusBadRequest, "re-entering the password"},
		{"unverified TLS to plain text", `{"tls_mode":"off"}`, http.StatusUnprocessableEntity, "nut_cleartext_credentials_confirm_required"},
		{"renamed", `{"name":"nut02"}`, http.StatusOK, `"id"`},
	} {
		status, body := doVeeamRequest(t, app, http.MethodPut, "/nut-servers/"+stored.ID.String(), "", tc.body)
		if status != tc.status || !strings.Contains(body, tc.want) {
			t.Errorf("%s: %d %s, want %d containing %q", tc.name, status, body, tc.status, tc.want)
		}
	}
	if n := dials.Load(); n != 0 {
		t.Errorf("dialed %d times: every refusal must come before the probe", n)
	}
}

func TestUpsIssues(t *testing.T) {
	t.Parallel()
	pf := func(v float64) *float64 { return &v }
	base := upsDeviceResponse{Name: "ups02", ServerName: "nut01", ServerEnabled: true, Reachability: "ok", State: "online"}
	with := func(f func(*upsDeviceResponse)) upsDeviceResponse { d := base; f(&d); return d }

	for _, tc := range []struct {
		name string
		dev  upsDeviceResponse
		want []string
	}{
		{"on mains", base, nil},
		{"on battery", with(func(d *upsDeviceResponse) {
			d.State, d.BatteryCharge, d.BatteryRuntime = "on_battery", pf(52), pf(580)
		}), []string{"ups_on_battery warn ups02 is on battery · 52% · 9m 40s left"}},
		{"low", with(func(d *upsDeviceResponse) { d.State, d.Flags = "low_battery", []string{"OB", "LB"} }),
			[]string{"ups_low_battery err ups02 is on battery and low"}},
		// upsmon -c fsd, NUT's documented shutdown test, runs on mains.
		{"forced shutdown on mains", with(func(d *upsDeviceResponse) { d.State, d.Flags = "low_battery", []string{"FSD", "OL"} }),
			[]string{"ups_low_battery err ups02 is shutting down (FSD)"}},
		// Could not look: only that, never the last known power state.
		{"unreachable while on battery", with(func(d *upsDeviceResponse) { d.State, d.Reachability = "on_battery", "unreachable" }),
			[]string{"ups_unreachable warn Nexara cannot read ups02 from nut01"}},
		{"old reading", with(func(d *upsDeviceResponse) { d.ReadingStale = true }), []string{"ups_unreachable warn Nexara cannot read ups02 from nut01"}},
		{"server disabled", with(func(d *upsDeviceResponse) { d.ServerEnabled, d.Reachability = false, "unreachable" }), nil},
		{"replace battery and overload", with(func(d *upsDeviceResponse) { d.Flags = []string{"OL", "RB", "OVER"} }),
			[]string{"ups_overload err ups02 reports an overload", "ups_replace_battery warn ups02 reports that its battery needs replacing"}},
		{"a node not protected, on mains", with(func(d *upsDeviceResponse) {
			d.Feeds = []upsFeedResponse{{Kind: "node", NodeName: "pve-03", Protection: "not_protected"}, {Kind: "node", NodeName: "pve-01", Protection: "protected"}}
		}), []string{"ups_node_unprotected warn pve-03 is powered by ups02, but no upsmon on it is logged in"}},
		{"a node not protected, UPS unreadable", with(func(d *upsDeviceResponse) {
			d.Reachability, d.Flags = "unreachable", []string{"OB"}
			d.Feeds = []upsFeedResponse{{Kind: "node", NodeName: "pve-03", ClusterName: "cluster01", Protection: "not_protected"}}
		}), []string{"ups_unreachable warn Nexara cannot read ups02 from nut01",
			"ups_node_unprotected warn pve-03 (cluster01) is powered by ups02, but no upsmon on it is logged in"}},
		{"a node not protected, on battery", with(func(d *upsDeviceResponse) {
			d.State, d.Flags, d.BatteryCharge, d.BatteryRuntime = "on_battery", []string{"OB"}, pf(52), pf(580)
			d.Feeds = []upsFeedResponse{{Kind: "node", NodeName: "pve-03", Protection: "not_protected"}}
		}), []string{"ups_on_battery warn ups02 is on battery · 52% · 9m 40s left",
			"ups_node_unprotected err pve-03 is powered by ups02, which is on battery, and no upsmon on it is logged in · 52% · 9m 40s left"}},
	} {
		var got []string
		for _, i := range upsIssues(tc.dev) {
			got = append(got, i.Type+" "+i.Severity+" "+i.Detail)
		}
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The estimate is labelled, and a reading older than the stale window is
// flagged, its state served as unknown rather than as the last one seen.
func TestBuildUpsDevice_EstimatesPowerAndFlagsStaleReadings(t *testing.T) {
	t.Parallel()
	h := NewNutHandler(nil, testEncryptionKey, nil)
	now := time.Now()
	open := map[string]time.Time{"on_battery": now.Add(-time.Hour)}
	row := upsTestRow(`{"ups.load":"38","ups.realpower.nominal":"900","ups.status":"OB"}`, now.Add(-time.Minute))
	row.State = "on_battery"
	d := h.buildUpsDevice(row, open, nil, now, false)
	if d.Realpower == nil || *d.Realpower != 342 || !d.RealpowerEstimated || d.ReadingStale ||
		d.State != "on_battery" || d.OnBatterySince == nil {
		t.Errorf("fresh: realpower %v estimated %v stale %v state %s since %v",
			d.Realpower, d.RealpowerEstimated, d.ReadingStale, d.State, d.OnBatterySince)
	}
	row = upsTestRow(`{"ups.realpower":"300"}`, now.Add(-10*time.Minute))
	row.State = "on_battery"
	d = h.buildUpsDevice(row, open, nil, now, false)
	if d.Realpower == nil || *d.Realpower != 300 || d.RealpowerEstimated || !d.ReadingStale ||
		d.State != "unknown" || d.LastKnownState != "on_battery" || d.OnBatterySince != nil {
		t.Errorf("stale: realpower %v estimated %v stale %v state %s (last %s) since %v",
			d.Realpower, d.RealpowerEstimated, d.ReadingStale, d.State, d.LastKnownState, d.OnBatterySince)
	}
}

// Through the real handler, the device list shows what a UPS powers only
// where the caller holds view:node on the cluster: a node feed, a login placed
// on a node and the host node (its loopback login too) elsewhere are left out,
// and so is every login a scoped caller cannot place, lest the gaps say which
// addresses are hidden nodes. A UPS never listed judges nobody.
func TestListDevices_ShowsWhatItPowersOnlyWhereTheCallerMaySee(t *testing.T) {
	t.Parallel()
	seen, hidden := uuid.New(), uuid.New()
	listed := upsTestRow(`{"ups.status":"OL"}`, time.Now())
	listed.ClientsListedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	listed.ServerHostClusterID = pgtype.UUID{Bytes: hidden, Valid: true}
	listed.ServerHostNodeName = pgtype.Text{String: "pve-09", Valid: true}
	unlisted := upsTestRow(`{"ups.status":"OL"}`, time.Now())
	node := func(ups, cluster uuid.UUID, name string) db.ListUpsFeedsRow {
		return db.ListUpsFeedsRow{ID: uuid.New(), UpsID: ups, Source: "found",
			ClusterID: pgtype.UUID{Bytes: cluster, Valid: true}, NodeName: pgtype.Text{String: name, Valid: true},
			NodeStatus: pgtype.Text{String: "online", Valid: true}, NodeStatusCurrent: true,
			ClusterName: pgtype.Text{String: "cluster01", Valid: true}}
	}
	stale := func(f db.ListUpsFeedsRow) db.ListUpsFeedsRow { f.NodeStatusCurrent = false; return f } // a cluster out of reach
	login := func(addr string) db.UpsClient { return db.UpsClient{UpsID: listed.ID, Address: addr, Connections: 1} }
	addr := func(cluster uuid.UUID, name, a string) db.ListNodeLoginAddressesRow {
		return db.ListNodeLoginAddressesRow{ClusterID: cluster, NodeName: name, Address: a}
	}
	dbtx := &auditRowsDBTX{rowsFor: map[string][]any{
		"ListUpsDevices": {listed, unlisted},
		"ListUpsFeeds": {
			node(listed.ID, seen, "pve-01"), node(listed.ID, seen, "pve-02"), node(listed.ID, hidden, "pve-09"),
			stale(node(listed.ID, seen, "pve-04")),
			db.ListUpsFeedsRow{ID: uuid.New(), UpsID: listed.ID, Label: "Core switch", Source: "manual"},
			node(unlisted.ID, seen, "pve-03"),
		},
		"ListUpsClients": {login("192.0.2.11"), login("192.0.2.19"), login("127.0.0.1"), login("192.0.2.99")},
		"ListNodeLoginAddresses": {
			addr(seen, "pve-01", "192.0.2.11"), addr(seen, "pve-02", "192.0.2.12"),
			addr(seen, "pve-03", "192.0.2.13"), addr(seen, "pve-04", "192.0.2.14"), addr(hidden, "pve-09", "192.0.2.19"),
		},
	}}
	h := NewNutHandler(db.New(dbtx), testEncryptionKey, nil)
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Use(func(c fiber.Ctx) error {
		c.Locals("user_id", uuid.New())
		c.Locals("rbac_engine", clusterGrantEngine{"view", "node", seen})
		return c.Next()
	})
	app.Get("/ups-devices", func(c fiber.Ctx) error { return h.ListDevices(c, nil) })
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/ups-devices", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var got struct {
		Items []upsDeviceResponse `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil || len(got.Items) != 2 {
		t.Fatalf("status %d, %d devices, %v", resp.StatusCode, len(got.Items), err)
	}
	feeds := func(d upsDeviceResponse) (out []string) {
		for _, f := range d.Feeds {
			out = append(out, f.Kind+" "+f.NodeName+f.Label+" "+f.Protection)
		}
		return out
	}
	d := got.Items[0]
	clients := make([]string, 0, len(d.Clients))
	for _, c := range d.Clients {
		clients = append(clients, c.Address+" "+c.NodeName)
	}
	issues := make([]string, 0, len(d.Issues))
	for _, i := range d.Issues {
		issues = append(issues, i.Type+" "+i.Target)
	}
	if want := []string{"node pve-01 protected", "node pve-02 not_protected", "node pve-04 unchecked", "label Core switch "}; !slices.Equal(feeds(d), want) {
		t.Errorf("feeds = %q, want %q", feeds(d), want)
	}
	if want := []string{"192.0.2.11 pve-01"}; !slices.Equal(clients, want) {
		t.Errorf("logins = %q, want %q", clients, want)
	}
	if want := []string{"ups_node_unprotected pve-02 (cluster01)"}; !slices.Equal(issues, want) || d.HostNode != nil {
		t.Errorf("issues = %q, host node %v; want %q and no host node", issues, d.HostNode, want)
	}
	if want := []string{"node pve-03 unchecked"}; !slices.Equal(feeds(got.Items[1]), want) {
		t.Errorf("never listed: feeds = %q, want %q", feeds(got.Items[1]), want)
	}
}

func upsTestRow(vars string, readAt time.Time) db.ListUpsDevicesRow {
	return db.ListUpsDevicesRow{
		ID: uuid.New(), UpsName: "ups01", State: "online", Reachability: "ok", ServerEnabled: true,
		Status: "OL", Vars: json.RawMessage(vars), VarDescriptions: json.RawMessage(`{}`),
		LastReadingAt: pgtype.Timestamptz{Time: readAt, Valid: true},
	}
}
