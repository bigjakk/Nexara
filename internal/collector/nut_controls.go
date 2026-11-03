package collector

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/nut"
	"github.com/bigjakk/nexara/internal/upscontrol"
)

const (
	// nutControlsInterval is how often each UPS's commands and writable
	// variables are listed again; a new driver lists them at once.
	nutControlsInterval = 10 * time.Minute
	// nutControlsPerPoll bounds the GET CMDDESC, GET TYPE, LIST ENUM and LIST
	// RANGE round trips per poll, as nutDescribePerPoll does for GET DESC.
	nutControlsPerPoll = 16
	// maxStoredControls caps the commands, and the writable variables, kept
	// per UPS: a 48-outlet PDU lists a few hundred commands.
	maxStoredControls = 512
)

// upsControls is the poller's copy of a UPS's commands and writable variables.
type upsControls struct {
	commands  []upscontrol.Command
	described map[string]bool
	vars      map[string]upscontrol.Var
	// tried holds the variables whose type upsd would not give, so each is
	// asked once per listing rather than every poll.
	tried    map[string]bool
	listedAt time.Time
	driver   string
}

func loadControls(r db.UpsDevice) upsControls {
	c := upsControls{described: map[string]bool{}, vars: map[string]upscontrol.Var{}, tried: map[string]bool{},
		driver: r.CommandsDriver}
	if r.CommandsListedAt.Valid {
		c.listedAt = r.CommandsListedAt.Time
	}
	_ = json.Unmarshal(r.Commands, &c.commands)
	_ = json.Unmarshal(r.RwVars, &c.vars)
	if c.vars == nil {
		c.vars = map[string]upscontrol.Var{}
	}
	for _, cmd := range c.commands {
		if cmd.Description != "" {
			c.described[cmd.Name] = true
		}
	}
	return c
}

// listControls lists each UPS read fresh this poll again once its listing is
// nutControlsInterval old or its driver changed, then reads what it lacks of
// each entry within a per-poll budget, storing a UPS's lists when they change.
// A UPS whose driver is stale is skipped: upsd answers neither list for it.
func (p *NutPoller) listControls(ctx context.Context, st *nutServerState, now time.Time) (changed bool) {
	budget := nutControlsPerPoll
	for _, name := range sortedDeviceNames(st.devices) {
		d := st.devices[name]
		if !d.lastFresh.Equal(now) {
			continue
		}
		c := &d.controls
		dirty := false
		if c.listedAt.IsZero() || now.Sub(c.listedAt) >= nutControlsInterval || c.driver != d.driver {
			newDriver := c.driver != d.driver && !c.listedAt.IsZero()
			// Stamped first, so a list upsd refuses, or one this client cannot
			// read, is asked again at the next interval rather than every poll.
			c.listedAt, c.driver = now, d.driver
			ok, err := p.relistControls(ctx, st.conn, name, c, newDriver)
			if err != nil {
				return changed // the connection is in trouble; the next poll finds out
			}
			if !ok {
				continue
			}
			dirty = true
		}
		var err error
		if budget, dirty, err = p.detailControls(ctx, st.conn, name, c, budget, dirty); err != nil {
			return changed
		}
		if dirty {
			cmds, _ := json.Marshal(c.commands)
			vars, _ := json.Marshal(c.vars)
			if err := p.q.SetUpsDeviceControls(ctx, db.SetUpsDeviceControlsParams{
				ID: d.id, Commands: cmds, RwVars: vars, ListedAt: pgtype.Timestamptz{Time: c.listedAt, Valid: true},
				Driver: c.driver,
			}); err != nil {
				p.logger.Warn("nut: storing commands failed", "ups", name, "error", err)
			}
			changed = true
		}
		if budget == 0 {
			return changed
		}
	}
	return changed
}

// relistControls replaces c's lists with LIST CMD and LIST RW, keeping what
// was read of the entries that remain unless the driver changed. ok is false
// when upsd refused a list, which leaves the lists as they were.
func (p *NutPoller) relistControls(ctx context.Context, conn *nut.Client, ups string, c *upsControls, newDriver bool) (ok bool, err error) {
	if newDriver {
		// Before listing, so a failed listing still reads everything again.
		c.described, c.vars = map[string]bool{}, map[string]upscontrol.Var{}
	}
	names, err := conn.ListCommands(ctx, ups)
	rw, rwErr := map[string]string(nil), error(nil)
	if err == nil {
		rw, rwErr = conn.ListRW(ctx, ups)
	}
	var pe *nut.ProtocolError
	switch {
	case errors.As(err, &pe), errors.As(rwErr, &pe):
		return false, nil
	case err != nil:
		return false, err
	case rwErr != nil:
		return false, rwErr
	}
	old := map[string]string{}
	for _, cmd := range c.commands {
		old[cmd.Name] = cmd.Description
	}
	slices.Sort(names)
	c.commands = c.commands[:0]
	for _, n := range names[:min(len(names), maxStoredControls)] {
		c.commands = append(c.commands, upscontrol.Command{Name: n, Description: old[n]})
	}
	vars := map[string]upscontrol.Var{}
	for _, n := range sortedVarNames(rw)[:min(len(rw), maxStoredControls)] {
		vars[n] = c.vars[n]
	}
	c.vars, c.tried = vars, map[string]bool{}
	return true, nil
}

// detailControls reads command descriptions and variable types c lacks, as
// many as budget allows, and returns what is left of the budget.
func (p *NutPoller) detailControls(ctx context.Context, conn *nut.Client, ups string, c *upsControls, budget int, dirty bool) (left int, changed bool, err error) {
	var pe *nut.ProtocolError
	for i := range c.commands {
		if budget == 0 {
			return 0, dirty, nil
		}
		if c.described[c.commands[i].Name] {
			continue
		}
		// Marked first, as each variable is below: a reply this client cannot
		// read must not hold up every entry after it, poll after poll.
		c.described[c.commands[i].Name] = true
		desc, err := conn.CommandDescription(ctx, ups, c.commands[i].Name)
		budget--
		if err != nil {
			if !errors.As(err, &pe) {
				return budget, dirty, err
			}
			continue
		}
		if desc = clipText(desc, maxStoredValue); desc != c.commands[i].Description {
			c.commands[i].Description, dirty = desc, true
		}
	}
	for _, name := range slices.Sorted(maps.Keys(c.vars)) {
		if budget == 0 {
			return 0, dirty, nil
		}
		if c.vars[name].Type != "" || c.tried[name] {
			continue
		}
		c.tried[name] = true
		t, err := conn.VarType(ctx, ups, name)
		budget--
		if err != nil {
			if !errors.As(err, &pe) {
				return budget, dirty, err
			}
			continue
		}
		var enum []string
		var ranges []nut.Range
		switch {
		case t.Enum && budget > 0:
			enum, err = conn.ListEnum(ctx, ups, name)
			budget--
		case t.Range && budget > 0:
			ranges, err = conn.ListRange(ctx, ups, name)
			budget--
		case t.Enum || t.Range:
			c.tried[name] = false // its list waits for the next poll's budget
			continue
		}
		if err != nil {
			if !errors.As(err, &pe) {
				return budget, dirty, err
			}
			// Asked again after the next listing: stored empty, the list
			// would read as no allowed values for good.
			continue
		}
		c.vars[name], dirty = upscontrol.FromType(t, enum, ranges), true
	}
	return budget, dirty, nil
}
