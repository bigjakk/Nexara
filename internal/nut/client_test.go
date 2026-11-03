package nut

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bigjakk/nexara/internal/nut/nuttest"
)

func newFake(t *testing.T) (*nuttest.Server, Config) {
	t.Helper()
	s := nuttest.NewServer(t)
	s.SetDevice("ups01", nuttest.Device{
		Description: "Rack UPS",
		Vars:        map[string]string{"ups.status": "OL", "battery.charge": "100"},
		Descs:       map[string]string{"battery.charge": "Battery charge (percent of full)"},
	})
	s.SetDevice("ups02", nuttest.Device{Description: "Closet UPS"})
	host, port := s.Addr()
	return s, Config{Host: host, Port: port, Timeout: 2 * time.Second}
}

func mustDial(t *testing.T, cfg Config) *Client {
	t.Helper()
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestClient_Reads(t *testing.T) {
	t.Parallel()
	s, cfg := newFake(t)
	s.Fail("LIST VAR ups02", "DATA-STALE")
	s.SetDevice("ups03", nuttest.Device{}) // no desc in ups.conf
	c := mustDial(t, cfg)
	ctx := context.Background()

	release, protocol, err := c.Versions(ctx)
	if err != nil || release != "2.8.1" || protocol != "1.3" {
		t.Fatalf("Versions = %q, %q, %v", release, protocol, err)
	}
	ups, err := c.ListUPS(ctx)
	if err != nil || !slices.Equal(ups, []UPS{{"ups01", "Rack UPS"}, {"ups02", "Closet UPS"}, {"ups03", ""}}) {
		t.Fatalf("ListUPS = %v, %v", ups, err)
	}
	vars, err := c.ListVars(ctx, "ups01")
	if err != nil || vars["ups.status"] != "OL" || vars["battery.charge"] != "100" || len(vars) != 2 {
		t.Fatalf("ListVars = %v, %v", vars, err)
	}
	// ERR replies are typed and leave the connection usable for the next command.
	if _, err := c.ListVars(ctx, "ups02"); !IsStale(err) {
		t.Errorf("stale ListVars err = %v", err)
	}
	if _, err := c.ListVars(ctx, "nosuch"); !IsUnknownUPS(err) {
		t.Errorf("unknown ListVars err = %v", err)
	}
	// Asked as UPS01, upsd answers with its own spelling; a 2.7 upsd listening
	// on :: shows an IPv4 client IPv6-mapped.
	s.SetClients("ups01", "::ffff:192.0.2.12", "2001:db8::12", "2001:db8::12")
	want := []netip.Addr{netip.MustParseAddr("192.0.2.12"), netip.MustParseAddr("2001:db8::12"), netip.MustParseAddr("2001:db8::12")}
	if got, err := c.ListClients(ctx, "UPS01"); err != nil || !slices.Equal(got, want) {
		t.Errorf("ListClients = %v, %v; want %v", got, err, want)
	}
	if _, err := c.ListClients(ctx, "nosuch"); !IsUnknownUPS(err) {
		t.Errorf("unknown ListClients err = %v", err)
	}
	if d, err := c.VarDescription(ctx, "ups01", "battery.charge"); err != nil || d != "Battery charge (percent of full)" {
		t.Errorf("VarDescription = %q, %v", d, err)
	}
	if d, err := c.VarDescription(ctx, "ups01", "no.such.var"); err != nil || d != "" {
		t.Errorf("VarDescription(no text) = %q, %v; want \"\"", d, err)
	}
	// upsd cuts a reply past 512 bytes and drops its newline. Waiting for it
	// stalled to the deadline and broke the connection, so the poller redialled
	// and retried the variable every poll.
	for _, n := range []int{474, 475} { // the reply fits exactly, then is cut at exactly 512
		if d, err := c.VarDescription(ctx, "ups01", strings.Repeat("a", n)); err != nil || d != "" {
			t.Errorf("VarDescription(%d-byte name) = %q, %v; want \"\"", n, d, err)
		}
		if _, protocol, err := c.Versions(ctx); err != nil || protocol != "1.3" {
			t.Errorf("Versions after a %d-byte name = %q, %v", n, protocol, err)
		}
	}
}

func TestClient_StartTLS(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		serverTLS bool
		pin       func(real string) string
		verify    bool
		wantErr   string
	}{
		{name: "pinned", serverTLS: true, verify: true, pin: func(fp string) string { return strings.ToUpper(fp) }},
		{name: "wrong pin", serverTLS: true, pin: func(string) string { return strings.Repeat("ab", 32) }, wantErr: "fingerprint mismatch"},
		{name: "self-signed against the CA pool", serverTLS: true, verify: true, wantErr: "certificate"},
		{name: "server refuses", serverTLS: false, verify: true, wantErr: ErrTLSUnavailable.Error()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, cfg := newFake(t)
			fp := ""
			if tc.serverTLS {
				fp = s.EnableTLS(t)
			}
			cfg.TLS, cfg.VerifyTLS = TLSStartTLS, tc.verify
			if tc.pin != nil {
				cfg.Fingerprint = tc.pin(fp)
			}
			c, err := Dial(context.Background(), cfg)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Dial err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Dial: %v", err)
			}
			defer c.Close()
			if c.PeerFingerprint() != fp {
				t.Errorf("PeerFingerprint = %q, want %q", c.PeerFingerprint(), fp)
			}
			if _, err := c.ListUPS(context.Background()); err != nil {
				t.Errorf("ListUPS over TLS: %v", err)
			}
		})
	}
}

// A LOGIN would make the primary upsmon wait on Nexara before powering the UPS
// off. A read-only session through the public API must send only reads, and
// the choke point must refuse LOGIN, PRIMARY, MASTER and FSD on every
// connection, INSTCMD and SET on one not dialled for commands, and a line
// break smuggled into an argument, before writing anything.
func TestClient_SendsOnlyAllowedVerbs(t *testing.T) {
	t.Parallel()
	s, cfg := newFake(t)
	cfg.Username, cfg.Password = "nexara", `pa ss"\wo#rd`
	ctx := context.Background()

	c := mustDial(t, cfg)
	_, _, _ = c.Versions(ctx)
	_, _ = c.ListUPS(ctx)
	_, _ = c.ListVars(ctx, "ups01")
	_, _ = c.ListClients(ctx, "ups01")
	_, _ = c.VarDescription(ctx, "ups01", "battery.charge")
	_ = c.Close()
	got := s.Received()
	for _, line := range got {
		if verb := strings.Fields(line)[0]; !allowedVerbs[verb] {
			t.Errorf("session sent %q", line)
		}
	}
	if !slices.Contains(got, `PASSWORD "pa ss\"\\wo\#rd"`) {
		t.Errorf("password not quoted for upsd; received %q", got)
	}

	for _, commands := range []bool{false, true} {
		cfg.Commands = commands
		c2 := mustDial(t, cfg)
		before := len(s.Received())
		refused := [][]string{
			{"LOGIN", "ups01"}, {"PRIMARY", "ups01"}, {"MASTER", "ups01"}, {"FSD", "ups01"},
			{"LIST", "VAR", "ups01\nLOGIN ups01"}, {"SET", "VAR", "ups01", "x", "1\nFSD ups01"},
		}
		if !commands {
			refused = append(refused, []string{"INSTCMD", "ups01", "load.off"}, []string{"SET", "TRACKING", "ON"})
		}
		for _, cmd := range refused {
			if err := c2.write(ctx, cmd[0], cmd[1:]...); err == nil {
				t.Errorf("commands=%v: write(%q) was allowed", commands, cmd)
			} else if !strings.ContainsAny(strings.Join(cmd, " "), "\n") && !errors.Is(err, ErrVerbNotAllowed) {
				t.Errorf("commands=%v: write(%q) err = %v", commands, cmd, err)
			}
		}
		// A round trip after the refusals: once its reply is back, anything a
		// refused write had sent would already be in the fake's log ahead of it.
		if _, _, err := c2.Versions(ctx); err != nil {
			t.Fatal(err)
		}
		if after := s.Received()[before:]; !slices.Equal(after, []string{"VER", "NETVER"}) {
			t.Errorf("commands=%v: server received %q after the refusals, want only VER and NETVER", commands, after)
		}
	}
}

// upsd's tokenizer makes an unquoted = a word of its own, wherever it falls
// (a password "ab=cd" arrived as three words): one holding = must still reach
// upsd as one unchanged word.
func TestClient_SendsCredentialsAsUpsdReadsThem(t *testing.T) {
	t.Parallel()
	for _, password := range []string{"ab=cd", "=ab"} {
		s, cfg := newFake(t)
		cfg.Username, cfg.Password = "nexara", password
		c, err := Dial(context.Background(), cfg)
		if err != nil {
			t.Errorf("%q: Dial: %v", password, err)
			continue
		}
		_ = c.Close()
		if got := s.Passwords(); !slices.Equal(got, []string{password}) {
			t.Errorf("%q: upsd would read %q", password, got)
		}
	}
}

func TestSplitLine(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in      string
		want    []string
		wantErr bool
	}{
		{in: `VAR ups01 ups.status "OL"`, want: []string{"VAR", "ups01", "ups.status", "OL"}},
		{in: `UPS ups01 "Rack UPS"`, want: []string{"UPS", "ups01", "Rack UPS"}},
		{in: `VAR u x "say \"hi\" \\ there"`, want: []string{"VAR", "u", "x", `say "hi" \ there`}},
		{in: `VAR u x ""`, want: []string{"VAR", "u", "x", ""}},
		{in: "ERR  DATA-STALE\t", want: []string{"ERR", "DATA-STALE"}},
		{in: `VAR u x "unterminated`, wantErr: true},
	} {
		got, err := splitLine(tc.in)
		if (err != nil) != tc.wantErr || !slices.Equal(got, tc.want) {
			t.Errorf("splitLine(%q) = %q, %v", tc.in, got, err)
		}
	}
}

func TestParseStatus(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		raw   string
		state State
		flags []string
	}{
		{"OL", StateOnline, []string{"OL"}},
		{"OL CHRG", StateOnline, []string{"OL", "CHRG"}},
		{"OB DISCHRG", StateOnBattery, []string{"OB", "DISCHRG"}},
		{"OB DISCHRG LB", StateLowBattery, []string{"OB", "DISCHRG", "LB"}},
		{"OL LB", StateOnline, []string{"OL", "LB"}}, // upsmon: LB on line power is not critical
		{"FSD OL", StateLowBattery, []string{"FSD", "OL"}},
		{"OFF", StateOff, []string{"OFF"}},
		{"OL OL RB", StateOnline, []string{"OL", "RB"}},
		{"", StateUnknown, nil},
		{"BYPASS", StateUnknown, []string{"BYPASS"}},
	} {
		got := ParseStatus(tc.raw)
		if got.State != tc.state || !slices.Equal(got.Flags, tc.flags) {
			t.Errorf("ParseStatus(%q) = %v %q, want %v %q", tc.raw, got.State, got.Flags, tc.state, tc.flags)
		}
	}
}
