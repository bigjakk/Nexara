package nuttest

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// The fake stands in for upsd, so this table pins it to what a real one said.
// Every "want" was captured on 2026-11-02 from upsd 2.8.1 (Debian trixie
// nut-server, dummy-ups driver), all on one connection as here. The real
// LIST VAR carried more variables; the framing, quoting and sort order are
// what clients rely on. DRIVER-NOT-CONNECTED was captured with the driver
// stopped and is injected here; ups03 was captured with no desc in ups.conf.
// LIST CLIENT was captured on 2026-11-03 with two upsmon logins from one
// host, whose address is replaced with TEST-NET here. The command sessions
// were captured on 2026-11-03 from dummy-ups's ups01, with an account allowed
// SET and one command ups01 lacks, the same with a wrong password sent first,
// and one allowed every command. upsd's tracking ids differ per run:
// "OK TRACKING *" takes any, and $ID sends the last one.
func TestFake_MatchesRealUpsd(t *testing.T) {
	t.Parallel()
	s := NewServer(t)
	s.SetDevice("ups01", Device{
		Description: "Rack UPS",
		Vars:        map[string]string{"ups.status": "OL", "battery.charge": "100", "device.mfr": "Dummy Manufacturer"},
		Descs:       map[string]string{"battery.charge": "Battery charge (percent of full)"},
		Clients:     []string{"192.0.2.12", "192.0.2.12"},
	})
	s.SetDevice("ups02", Device{Description: "Closet UPS"})
	s.SetDevice("ups03", Device{})
	s.Fail("LIST VAR ups02", "DRIVER-NOT-CONNECTED")
	long := strings.Repeat("a", 480)
	cut := ("DESC ups01 " + long + ` "Description unavailable"`)[:512]

	replay(t, s, []exchange{
		{"VER", []string{"Network UPS Tools upsd 2.8.1 - https://www.networkupstools.org/"}},
		{"NETVER", []string{"1.3"}},
		{"PROTVER", []string{"1.3"}},
		{"LIST UPS", []string{
			"BEGIN LIST UPS", `UPS ups01 "Rack UPS"`, `UPS ups02 "Closet UPS"`, `UPS ups03 "Description unavailable"`, "END LIST UPS",
		}},
		{"LIST VAR ups01", []string{
			"BEGIN LIST VAR ups01",
			`VAR ups01 battery.charge "100"`,
			`VAR ups01 device.mfr "Dummy Manufacturer"`,
			`VAR ups01 ups.status "OL"`,
			"END LIST VAR ups01",
		}},
		{"LIST VAR UPS01", []string{
			"BEGIN LIST VAR UPS01",
			`VAR UPS01 battery.charge "100"`,
			`VAR UPS01 device.mfr "Dummy Manufacturer"`,
			`VAR UPS01 ups.status "OL"`,
			"END LIST VAR UPS01",
		}},
		{"LIST VAR nosuch", []string{"ERR UNKNOWN-UPS"}},
		{"LIST CLIENT ups01", []string{
			"BEGIN LIST CLIENT ups01", "CLIENT ups01 192.0.2.12", "CLIENT ups01 192.0.2.12", "END LIST CLIENT ups01",
		}},
		{"LIST CLIENT UPS01", []string{
			"BEGIN LIST CLIENT UPS01", "CLIENT ups01 192.0.2.12", "CLIENT ups01 192.0.2.12", "END LIST CLIENT UPS01",
		}},
		{"LIST CLIENT ups02", []string{"BEGIN LIST CLIENT ups02", "END LIST CLIENT ups02"}},
		{"LIST CLIENT nosuch", []string{"ERR UNKNOWN-UPS"}},
		{"LIST VAR ups02", []string{"ERR DRIVER-NOT-CONNECTED"}},
		{"GET DESC ups01 battery.charge", []string{`DESC ups01 battery.charge "Battery charge (percent of full)"`}},
		{"GET DESC ups01 no.such.var", []string{`DESC ups01 no.such.var "Description unavailable"`}},
		// A reply past upsd's 512-byte buffer goes out cut, without its newline,
		// so the next reply runs on from the cut (captured 2026-11-03).
		{"GET DESC ups01 " + long, nil},
		{"NETVER", []string{cut + "1.3"}},
		{"FOOBAR", []string{"ERR UNKNOWN-COMMAND"}},
		{"STARTTLS", []string{"ERR FEATURE-NOT-CONFIGURED"}},
		{"USERNAME nexara", []string{"OK"}},
		// An unquoted = is a word of its own, so this PASSWORD has three
		// arguments; nothing is set, and the next PASSWORD is accepted.
		{"PASSWORD ab=cd", []string{"ERR INVALID-ARGUMENT"}},
		{"PASSWORD", []string{"ERR INVALID-ARGUMENT"}},
		{`PASSWORD "pa ss\"\\wo\#rd"`, []string{"OK"}},
		{"LOGOUT", []string{"OK Goodbye"}},
	})

	c := NewServer(t)
	c.SetDevice("ups01", Device{
		Vars:     map[string]string{"battery.charge.low": "10", "device.model": "Dummy UPS"},
		Types:    map[string]string{"battery.charge.low": "RW NUMBER", "device.model": "RW STRING:32"},
		Commands: []string{"driver.killpower", "driver.reload", "driver.reload-or-error", "driver.reload-or-exit", "load.off"},
		CmdDescs: map[string]string{"load.off": "Turn off the load immediately"},
		Outcomes: map[string]string{"load.off": "ERR UNKNOWN"},
	})
	c.SetUser("nexara", User{Password: "test-only-password", SET: true, Instcmds: []string{"test.battery.start.quick"}})
	c.SetUser("capture", User{Password: "capture-only", SET: true, Instcmds: []string{"ALL"}})
	replay(t, c, []exchange{
		{"LIST CMD ups01", []string{
			"BEGIN LIST CMD ups01", "CMD ups01 driver.killpower", "CMD ups01 driver.reload",
			"CMD ups01 driver.reload-or-error", "CMD ups01 driver.reload-or-exit", "CMD ups01 load.off", "END LIST CMD ups01",
		}},
		{"GET CMDDESC ups01 load.off", []string{`CMDDESC ups01 load.off "Turn off the load immediately"`}},
		{"GET CMDDESC ups01 no.such.cmd", []string{`CMDDESC ups01 no.such.cmd "Description unavailable"`}},
		{"GET TYPE ups01 battery.charge.low", []string{"TYPE ups01 battery.charge.low RW NUMBER"}},
		{"GET TYPE ups01 device.model", []string{"TYPE ups01 device.model RW STRING:32"}},
		{"GET TYPE ups01 no.such.var", []string{"ERR VAR-NOT-SUPPORTED"}},
		{"LIST ENUM ups01 battery.charge.low", []string{"BEGIN LIST ENUM ups01 battery.charge.low", "END LIST ENUM ups01 battery.charge.low"}},
		{"LIST RANGE ups01 battery.charge.low", []string{"BEGIN LIST RANGE ups01 battery.charge.low", "END LIST RANGE ups01 battery.charge.low"}},
		{"GET TRACKING", []string{"OFF"}},
		{"SET TRACKING ON", []string{"ERR USERNAME-REQUIRED"}},
		{"INSTCMD ups01 driver.reload", []string{"ERR USERNAME-REQUIRED"}},
		{"USERNAME nexara", []string{"OK"}},
		{"SET TRACKING ON", []string{"ERR PASSWORD-REQUIRED"}},
		{"PASSWORD test-only-password", []string{"OK"}},
		{"SET TRACKING ON", []string{"OK"}},
		{"GET TRACKING", []string{"ON"}},
		{"INSTCMD ups01 driver.reload", []string{"ERR ACCESS-DENIED"}},
		{"INSTCMD ups01 test.battery.start.quick", []string{"ERR CMD-NOT-SUPPORTED"}},
		{"INSTCMD ups01 no.such.cmd", []string{"ERR CMD-NOT-SUPPORTED"}},
		{"INSTCMD ups01", []string{"ERR INVALID-ARGUMENT"}},
		{`SET VAR ups01 nexara.credential-check ""`, []string{"ERR VAR-NOT-SUPPORTED"}},
		{`SET VAR ups01 device.model "123456789012345678901234567890123"`, []string{"ERR TOO-LONG"}},
		{"SET VAR ups01 battery.charge.low", []string{"ERR INVALID-ARGUMENT"}},
		{`SET VAR ups01 battery.charge.low "20"`, []string{"OK TRACKING *"}},
		{"GET TRACKING $ID", []string{"SUCCESS"}},
		{"GET TRACKING $ID", []string{"SUCCESS"}},
		{"GET TRACKING 00000000-0000-0000-0000-000000000000", []string{"ERR UNKNOWN"}},
		{"SET TRACKING OFF", []string{"OK"}},
		{"GET TRACKING $ID", []string{"ERR FEATURE-NOT-CONFIGURED"}},
		{`SET VAR ups01 battery.charge.low "10"`, []string{"OK"}},
		{"LOGOUT", []string{"OK Goodbye"}},
	})
	replay(t, c, []exchange{
		{"USERNAME nexara", []string{"OK"}},
		{"PASSWORD wrong", []string{"OK"}},
		{"USERNAME other", []string{"ERR ALREADY-SET-USERNAME"}},
		{"PASSWORD test-only-password", []string{"ERR ALREADY-SET-PASSWORD"}},
		{`SET VAR ups01 nexara.credential-check ""`, []string{"ERR ACCESS-DENIED"}},
	})
	replay(t, c, []exchange{
		{"SET TRACKING ON", []string{"ERR USERNAME-REQUIRED"}},
		{"USERNAME capture", []string{"OK"}},
		{"PASSWORD capture-only", []string{"OK"}},
		{"INSTCMD ups01 driver.reload", []string{"OK"}},
		{"SET TRACKING ON", []string{"OK"}},
		{"INSTCMD ups01 load.off 30", []string{"OK TRACKING *"}},
		{"GET TRACKING $ID", []string{"ERR UNKNOWN"}},
		{"GET TRACKING $ID", []string{"ERR UNKNOWN"}},
	})
}

type exchange struct {
	send string
	want []string
}

// replay runs one session on its own connection.
func replay(t *testing.T, s *Server, session []exchange) {
	t.Helper()
	host, port := s.Addr()
	conn, err := net.Dial("tcp", net.JoinHostPort(host, fmt.Sprint(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(conn)
	id := ""
	for _, tc := range session {
		send := strings.ReplaceAll(tc.send, "$ID", id)
		if _, err := fmt.Fprintf(conn, "%s\n", send); err != nil {
			t.Fatal(err)
		}
		for _, want := range tc.want {
			got, err := r.ReadString('\n')
			if err != nil {
				t.Fatalf("%s: reading %q: %v", send, want, err)
			}
			got = strings.TrimRight(got, "\n")
			if rest, ok := strings.CutPrefix(got, "OK TRACKING "); ok && want == "OK TRACKING *" {
				id = rest
			} else if got != want {
				t.Errorf("%s: got %q, want %q", send, got, want)
			}
		}
	}
}
