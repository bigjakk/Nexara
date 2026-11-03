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
// host, whose address is replaced with TEST-NET here.
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

	host, port := s.Addr()
	conn, err := net.Dial("tcp", net.JoinHostPort(host, fmt.Sprint(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(conn)
	long := strings.Repeat("a", 480)
	cut := ("DESC ups01 " + long + ` "Description unavailable"`)[:512]

	for _, tc := range []struct {
		send string
		want []string
	}{
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
	} {
		if _, err := fmt.Fprintf(conn, "%s\n", tc.send); err != nil {
			t.Fatal(err)
		}
		for _, want := range tc.want {
			got, err := r.ReadString('\n')
			if err != nil {
				t.Fatalf("%s: reading %q: %v", tc.send, want, err)
			}
			if got = strings.TrimRight(got, "\n"); got != want {
				t.Errorf("%s: got %q, want %q", tc.send, got, want)
			}
		}
	}
}
