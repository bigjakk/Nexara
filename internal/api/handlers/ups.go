package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/nut"
	"github.com/bigjakk/nexara/internal/upscontrol"
	"github.com/bigjakk/nexara/internal/upsfeed"
)

// upsDeviceResponse is one UPS as the Power page shows it: identity, state,
// the readings it lives on, and the health issues it raises.
type upsDeviceResponse struct {
	ID                 uuid.UUID  `json:"id"`
	ServerID           uuid.UUID  `json:"server_id"`
	ServerName         string     `json:"server_name"`
	ServerEnabled      bool       `json:"server_enabled"`
	Name               string     `json:"name"`
	Description        string     `json:"description"`
	Manufacturer       string     `json:"manufacturer"`
	Model              string     `json:"model"`
	Serial             string     `json:"serial"`
	Firmware           string     `json:"firmware"`
	Driver             string     `json:"driver"`
	Status             string     `json:"status"`
	Flags              []string   `json:"flags"`
	State              string     `json:"state"`
	LastKnownState     string     `json:"last_known_state"`
	Reachability       string     `json:"reachability"`
	ReadingStale       bool       `json:"reading_stale"`
	LastReadingAt      *time.Time `json:"last_reading_at"`
	OnBatterySince     *time.Time `json:"on_battery_since"`
	BatteryCharge      *float64   `json:"battery_charge"`
	BatteryChargeLow   *float64   `json:"battery_charge_low"`
	BatteryRuntime     *float64   `json:"battery_runtime"`
	BatteryRuntimeLow  *float64   `json:"battery_runtime_low"`
	BatteryVoltage     *float64   `json:"battery_voltage"`
	BatteryType        string     `json:"battery_type"`
	BatteryDate        string     `json:"battery_date"`
	Load               *float64   `json:"load"`
	Realpower          *float64   `json:"realpower"`
	RealpowerEstimated bool       `json:"realpower_estimated"`
	RealpowerNominal   *float64   `json:"realpower_nominal"`
	InputVoltage       *float64   `json:"input_voltage"`
	InputFrequency     *float64   `json:"input_frequency"`
	InputTransferLow   *float64   `json:"input_transfer_low"`
	InputTransferHigh  *float64   `json:"input_transfer_high"`
	OutputVoltage      *float64   `json:"output_voltage"`
	Temperature        *float64   `json:"temperature"`
	TestResult         string     `json:"test_result"`
	// What the UPS powers and who is logged in to it, as far as the caller may
	// see: node feeds, logins and the host node only for clusters whose nodes
	// the caller can view.
	Feeds           []upsFeedResponse     `json:"feeds"`
	Clients         []upsClientResponse   `json:"clients"`
	ClientsListedAt *time.Time            `json:"clients_listed_at"`
	HostNode        *upsNodeRef           `json:"host_node"`
	Issues          []healthIssueResponse `json:"issues"`
	Vars            map[string]string     `json:"vars,omitempty"`
	VarDescriptions map[string]string     `json:"var_descriptions,omitempty"`
	// The commands and settings the poller listed, on the detail only; nil
	// until the first listing.
	Commands         []upsCommandResponse  `json:"commands,omitempty"`
	Writable         []upsVariableResponse `json:"writable,omitempty"`
	CommandsListedAt *time.Time            `json:"commands_listed_at,omitempty"`
}

// upsCommandResponse is one instant command, with what running it asks for.
type upsCommandResponse struct {
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Tier        upscontrol.Tier    `json:"tier"`
	Confirm     upscontrol.Confirm `json:"confirm"`
}

// upsVariableResponse is one writable variable: its value, its type, whether
// Nexara offers it for editing and why not, and what setting it asks for.
type upsVariableResponse struct {
	Name     string   `json:"name"`
	Value    string   `json:"value"`
	Type     string   `json:"type"`
	MaxLen   int      `json:"max_len,omitempty"`
	Enum     []string `json:"enum,omitempty"`
	Ranges   [][2]int `json:"ranges,omitempty"`
	Editable bool     `json:"editable"`
	Reason   string   `json:"reason,omitempty"`
	// AffectsPower: setting it is gated as cutting power is (execute:ups, and
	// manage:node on every cluster the UPS powers).
	AffectsPower bool               `json:"affects_power"`
	Confirm      upscontrol.Confirm `json:"confirm"`
}

// ListDevices handles GET /api/v1/ups-devices.
func (h *NutHandler) ListDevices(c fiber.Ctx, _ *apischema.Params) error {
	rows, err := h.queries.ListUpsDevices(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list UPS devices")
	}
	open, err := h.openEpisodes(c)
	if err != nil {
		return err
	}
	var pw *upsPower
	if len(rows) > 0 { // the pill polls this for every view:ups holder, NUT servers or not
		if pw, err = h.loadPower(c); err != nil {
			return err
		}
	}
	now := time.Now()
	resp := make([]upsDeviceResponse, 0, len(rows))
	for _, r := range rows {
		resp = append(resp, h.buildUpsDevice(r, open[r.ID], pw, now, false))
	}
	return RespondItems(c, resp)
}

// GetDevice handles GET /api/v1/ups-devices/:id.
func (h *NutHandler) GetDevice(c fiber.Ctx, p *apischema.Params) error {
	row, err := h.fetchDevice(c, p)
	if err != nil {
		return err
	}
	open, err := h.openEpisodes(c)
	if err != nil {
		return err
	}
	pw, err := h.loadPower(c)
	if err != nil {
		return err
	}
	return c.JSON(h.buildUpsDevice(row, open[row.ID], pw, time.Now(), true))
}

func (h *NutHandler) fetchDevice(c fiber.Ctx, p *apischema.Params) (db.ListUpsDevicesRow, error) {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return db.ListUpsDevicesRow{}, err
	}
	row, err := h.queries.GetUpsDevice(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.ListUpsDevicesRow{}, fiber.NewError(fiber.StatusNotFound, "UPS not found")
		}
		return db.ListUpsDevicesRow{}, fiber.NewError(fiber.StatusInternalServerError, "Failed to get UPS")
	}
	// The two queries select the same columns, so their rows convert.
	return db.ListUpsDevicesRow(row), nil
}

func (h *NutHandler) openEpisodes(c fiber.Ctx) (map[uuid.UUID]map[string]time.Time, error) {
	rows, err := h.queries.ListOpenUpsEvents(c.Context())
	if err != nil {
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to list UPS events")
	}
	out := map[uuid.UUID]map[string]time.Time{}
	for _, r := range rows {
		if out[r.UpsID] == nil {
			out[r.UpsID] = map[string]time.Time{}
		}
		out[r.UpsID][r.Kind] = r.StartedAt
	}
	return out, nil
}

func (h *NutHandler) buildUpsDevice(r db.ListUpsDevicesRow, open map[string]time.Time, pw *upsPower, now time.Time, detail bool) upsDeviceResponse {
	var vars map[string]string
	_ = json.Unmarshal(r.Vars, &vars)
	status := nut.ParseStatus(r.Status)
	d := upsDeviceResponse{
		ID: r.ID, ServerID: r.ServerID, ServerName: r.ServerName, ServerEnabled: r.ServerEnabled,
		Name: r.UpsName, Description: r.Description, Manufacturer: r.Manufacturer, Model: r.Model,
		Serial: r.Serial, Firmware: r.Firmware, Driver: r.Driver, Status: r.Status,
		Flags: status.Flags, State: r.State, Reachability: r.Reachability,
		BatteryCharge: upsVar(vars, "battery.charge"), BatteryChargeLow: upsVar(vars, "battery.charge.low"),
		BatteryRuntime: upsVar(vars, "battery.runtime"), BatteryRuntimeLow: upsVar(vars, "battery.runtime.low"),
		BatteryVoltage: upsVar(vars, "battery.voltage"), BatteryType: vars["battery.type"],
		BatteryDate: firstNonEmpty(vars["battery.date"], vars["battery.mfr.date"]),
		Load:        upsVar(vars, "ups.load"), Realpower: upsVar(vars, "ups.realpower"),
		RealpowerNominal: upsVar(vars, "ups.realpower.nominal"),
		InputVoltage:     upsVar(vars, "input.voltage"), InputFrequency: upsVar(vars, "input.frequency"),
		InputTransferLow: upsVar(vars, "input.transfer.low"), InputTransferHigh: upsVar(vars, "input.transfer.high"),
		OutputVoltage: upsVar(vars, "output.voltage"),
		Temperature:   upsVar(vars, "ups.temperature", "battery.temperature"),
		TestResult:    vars["ups.test.result"],
	}
	if d.Flags == nil {
		d.Flags = []string{}
	}
	// Many UPSes report load and the nominal rating but not real power. The
	// estimate is labelled so the UI never presents it as a measurement.
	if d.Realpower == nil && d.Load != nil && d.RealpowerNominal != nil {
		w := math.Round(*d.Load * *d.RealpowerNominal / 100)
		d.Realpower, d.RealpowerEstimated = &w, true
	}
	var readAt time.Time
	if r.LastReadingAt.Valid {
		readAt = r.LastReadingAt.Time
		d.LastReadingAt = &readAt
	}
	d.ReadingStale = readAt.IsZero() || now.Sub(readAt) > nut.StaleAfter(h.pollInterval)
	// Only a fresh reading from a polled server says what the UPS is doing
	// now. Anything else serves "unknown", with the last state kept apart, so
	// no consumer can show a UPS Nexara cannot read as on battery or on mains.
	d.LastKnownState = r.State
	if nut.ReadingCurrent(d.ServerEnabled, d.Reachability, readAt, h.pollInterval, now) {
		if t, ok := open["on_battery"]; ok {
			d.OnBatterySince = &t
		}
	} else {
		d.State = string(nut.StateUnknown)
	}
	d.Feeds, d.Clients = []upsFeedResponse{}, []upsClientResponse{}
	if pw != nil {
		pw.apply(&d, r)
	}
	d.Issues = upsIssues(d)
	if detail {
		d.Vars = vars
		_ = json.Unmarshal(r.VarDescriptions, &d.VarDescriptions)
		if r.CommandsListedAt.Valid {
			d.CommandsListedAt = &r.CommandsListedAt.Time
			d.Commands, d.Writable = upsControlsResponse(r, vars)
		}
	}
	return d
}

func upsControlsResponse(r db.ListUpsDevicesRow, vars map[string]string) ([]upsCommandResponse, []upsVariableResponse) {
	var cmds []upscontrol.Command
	_ = json.Unmarshal(r.Commands, &cmds)
	commands := make([]upsCommandResponse, 0, len(cmds))
	for _, c := range cmds {
		commands = append(commands, upsCommandResponse{
			Name: c.Name, Description: c.Description, Tier: upscontrol.CommandTier(c.Name), Confirm: upscontrol.CommandConfirm(c.Name),
		})
	}
	rw := writableVars(r)
	writable := make([]upsVariableResponse, 0, len(rw))
	for _, name := range slices.Sorted(maps.Keys(rw)) {
		v := rw[name]
		editable, reason := v.Editable()
		writable = append(writable, upsVariableResponse{
			Name: name, Value: vars[name], Type: v.Type, MaxLen: v.MaxLen, Enum: v.Enum, Ranges: v.Ranges,
			Editable: editable, Reason: reason, AffectsPower: upscontrol.VarAffectsPower(name), Confirm: upscontrol.VarConfirm(name),
		})
	}
	return commands, writable
}

// upsIssues are the health-pill entries a UPS raises. A UPS Nexara cannot
// read raises only "unreachable", never a stale on-battery or on-mains claim,
// and a disabled server raises nothing.
func upsIssues(d upsDeviceResponse) []healthIssueResponse {
	issues := []healthIssueResponse{}
	if !d.ServerEnabled {
		return issues
	}
	// The summary is the state in a few words, as the cluster issues have it
	// ("Node offline"); the detail says how much battery is left.
	add := func(kind, sev, summary, detail string) {
		issues = append(issues, healthIssueResponse{
			Type: kind, Severity: sev, Scope: "ups", Target: d.Name, Summary: summary, Detail: detail,
		})
	}
	unreadable := d.Reachability != "ok" || d.ReadingStale
	left := upsLeft(d)
	if unreadable {
		add("ups_unreachable", healthSevWarn, "UPS unreachable", fmt.Sprintf("Nexara cannot read %s from %s", d.Name, d.ServerName))
	} else {
		switch d.State {
		case string(nut.StateLowBattery):
			// FSD lands here whatever the line power, as upsmon treats it; say
			// which it is rather than "on battery" during a shutdown test on mains.
			switch {
			case !has(d.Flags, "FSD"):
				add("ups_low_battery", healthSevErr, "Low battery", d.Name+" is on battery and low"+left)
			case has(d.Flags, "OB"):
				add("ups_low_battery", healthSevErr, "Shutting down", d.Name+" is on battery and shutting down (FSD)"+left)
			default:
				add("ups_low_battery", healthSevErr, "Shutting down", d.Name+" is shutting down (FSD)")
			}
		case string(nut.StateOnBattery):
			add("ups_on_battery", healthSevWarn, "On battery", d.Name+" is on battery"+left)
		}
		if has(d.Flags, "OVER") {
			add("ups_overload", healthSevErr, "UPS overload", d.Name+" reports an overload")
		}
		if has(d.Flags, "RB") {
			add("ups_replace_battery", healthSevWarn, "Replace battery", d.Name+" reports that its battery needs replacing")
		}
	}
	// One per node the UPS powers that would lose power without shutting down:
	// a warning, and an error once the UPS is read on battery. The verdict holds
	// while the UPS cannot be read (P4), so the warning does too, but a stale
	// reading never says "on battery". The target names the cluster, so two
	// nodes of one name never share a row or a dismissal in the pill.
	for _, f := range d.Feeds {
		if f.Protection != string(upsfeed.NotProtected) {
			continue
		}
		node := f.NodeName
		if f.ClusterName != "" {
			node += " (" + f.ClusterName + ")"
		}
		sev, detail := healthSevWarn, node+" is powered by "+d.Name+", but no upsmon on it is logged in"
		if !unreadable && has(d.Flags, "OB") {
			sev, detail = healthSevErr, node+" is powered by "+d.Name+", which is on battery, and no upsmon on it is logged in"+left
		}
		issues = append(issues, healthIssueResponse{
			Type: "ups_node_unprotected", Severity: sev, Scope: "ups", Target: node, Summary: "Not protected", Detail: detail,
		})
	}
	return issues
}

func upsLeft(d upsDeviceResponse) string {
	if left := nut.Remaining(d.BatteryCharge, d.BatteryRuntime); left != "" {
		return " · " + left
	}
	return ""
}

func has(flags []string, f string) bool {
	for _, x := range flags {
		if x == f {
			return true
		}
	}
	return false
}

func upsVar(vars map[string]string, keys ...string) *float64 {
	if f, ok := nut.Number(vars, keys...); ok {
		return &f
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// upsRanges are the ranges a UPS chart may ask for, and where each is read
// from: raw readings for the short ones, the rollups for the long ones. 30d
// is UPS-only; the rollups are kept indefinitely, so it costs nothing.
var upsRanges = map[string]struct {
	window time.Duration
	source string
	step   time.Duration
}{
	"1h":  {time.Hour, "raw", 0},
	"6h":  {6 * time.Hour, "raw", 0},
	"24h": {24 * time.Hour, "5m", rollup5m},
	"7d":  {7 * 24 * time.Hour, "1h", rollup1h},
	"30d": {30 * 24 * time.Hour, "1h", rollup1h},
}

type upsMetricPoint struct {
	Timestamp      int64    `json:"timestamp"`
	Charge         *float64 `json:"charge"`
	Runtime        *float64 `json:"runtime"`
	BatteryVoltage *float64 `json:"battery_voltage"`
	Load           *float64 `json:"load"`
	Realpower      *float64 `json:"realpower"`
	InputLow       *float64 `json:"input_voltage_min"`
	InputHigh      *float64 `json:"input_voltage_max"`
	OutputVoltage  *float64 `json:"output_voltage"`
	InputFrequency *float64 `json:"input_frequency"`
	Temperature    *float64 `json:"temperature"`
	OnBattery      *bool    `json:"on_battery"`
}

// GetDeviceMetrics handles GET /api/v1/ups-devices/:id/metrics. Every range
// comes back as at most seriesPoints buckets, never raw rows: a chart fed a
// week of 10-second samples is what froze the SPA once before. Input voltage
// comes as each bucket's low and high, so a long range still shows the sags
// and the surges a TRIM corrects.
func (h *NutHandler) GetDeviceMetrics(c fiber.Ctx, p *apischema.Params) error {
	row, err := h.fetchDevice(c, p)
	if err != nil {
		return err
	}
	rng, ok := upsRanges[p.String("range")]
	if !ok {
		return fiber.NewError(fiber.StatusInternalServerError, "Unsupported metric range")
	}
	step := rng.step
	if step == 0 {
		step = h.pollInterval
	}
	bucket := max(rng.window/seriesPoints, step)
	rows, err := h.queries.GetUpsMetricSeries(c.Context(), db.GetUpsMetricSeriesParams{
		Source: rng.source, UpsID: row.ID, Since: time.Now().Add(-rng.window),
		BucketSeconds: int32(bucket / time.Second), //nolint:gosec // G115: at most 30d/360 = 7200
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to load UPS metrics")
	}
	points := make([]upsMetricPoint, 0, len(rows))
	for _, r := range rows {
		pt := upsMetricPoint{
			Timestamp: r.Bucket.UnixMilli(), Charge: f8(r.ChargeMin, 1), Runtime: f8(r.RuntimeMin, 0),
			BatteryVoltage: f8(r.BatteryVoltage, 2), Load: f8(r.LoadMax, 1), Realpower: f8(r.Realpower, 0),
			InputLow: f8(r.InputVoltageMin, 1), InputHigh: f8(r.InputVoltageMax, 1), OutputVoltage: f8(r.OutputVoltage, 1),
			InputFrequency: f8(r.InputFrequency, 2), Temperature: f8(r.TemperatureMax, 1),
		}
		if r.OnBattery.Valid {
			b := r.OnBattery.Bool
			pt.OnBattery = &b
		}
		points = append(points, pt)
	}
	points = withGaps(points,
		func(p upsMetricPoint) int64 { return p.Timestamp },
		func(ts int64) upsMetricPoint { return upsMetricPoint{Timestamp: ts} })
	return c.JSON(fiber.Map{"range": p.String("range"), "bucket_seconds": int64(bucket / time.Second), "points": points})
}

func f8(v pgtype.Float8, places int) *float64 {
	if !v.Valid {
		return nil
	}
	return roundTo(v.Float64, places)
}

// upsEventResponse is one power event.
type upsEventResponse struct {
	ID             uuid.UUID       `json:"id"`
	UpsID          uuid.UUID       `json:"ups_id"`
	UpsName        string          `json:"ups_name"`
	UpsDescription string          `json:"ups_description"`
	ServerName     string          `json:"server_name"`
	Kind           string          `json:"kind"`
	StartedAt      time.Time       `json:"started_at"`
	EndedAt        *time.Time      `json:"ended_at"`
	EndObserved    bool            `json:"end_observed"`
	Details        json.RawMessage `json:"details"`
}

// ListEvents handles GET /api/v1/ups-events, newest first. For the next page
// pass the last item's started_at and id as before and before_id.
func (h *NutHandler) ListEvents(c fiber.Ctx, p *apischema.Params) error {
	params := db.ListUpsEventsParams{RowLimit: int32(p.Int("limit"))} //nolint:gosec // G115: declared 1..200
	if raw, ok := p.OptString("ups_id"); ok {
		id, err := parseParamUUID(raw)
		if err != nil {
			return err
		}
		params.UpsID = pgtype.UUID{Bytes: id, Valid: true}
	}
	if kind, ok := p.OptString("kind"); ok {
		params.Kind = pgtype.Text{String: kind, Valid: true}
	}
	if raw, ok := p.OptString("before"); ok {
		at, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "before must be an RFC 3339 timestamp")
		}
		id, err := parseParamUUID(p.String("before_id"))
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "before needs before_id, the id of the last event on the previous page")
		}
		params.BeforeStartedAt = pgtype.Timestamptz{Time: at, Valid: true}
		params.BeforeID = pgtype.UUID{Bytes: id, Valid: true}
	}
	rows, err := h.queries.ListUpsEvents(c.Context(), params)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list UPS events")
	}
	items := make([]upsEventResponse, 0, len(rows))
	for _, r := range rows {
		e := upsEventResponse{
			ID: r.ID, UpsID: r.UpsID, UpsName: r.UpsName, UpsDescription: r.UpsDescription,
			ServerName: r.ServerName, Kind: r.Kind, StartedAt: r.StartedAt,
			EndObserved: r.EndObserved, Details: r.EventDetails,
		}
		if r.EndedAt.Valid {
			t := r.EndedAt.Time
			e.EndedAt = &t
		}
		items = append(items, e)
	}
	return RespondItems(c, items)
}
