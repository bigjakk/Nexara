// Package nuttest is an in-process fake upsd for tests. It answers the
// read-only commands internal/nut sends, in upsd 2.8.1's wire format (the
// fidelity table in fake_test.go holds what that server really said), and it
// records every command line it receives, so a test can assert what a client
// sent and what it never sent.
//
// It must not import internal/nut: that package's own tests use this one.
package nuttest

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"io"
	"math/big"
	"net"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// Banner is what upsd 2.8.1 answers VER with.
const Banner = "Network UPS Tools upsd 2.8.1 - https://www.networkupstools.org/"

// Device is one UPS the fake serves.
type Device struct {
	Description string
	Vars        map[string]string
	// Descs holds GET DESC answers. A variable missing here gets upsd's own
	// "Description unavailable".
	Descs map[string]string
	// Clients are the addresses logged in to the UPS, one per connection, as
	// LIST CLIENT lists them.
	Clients []string
}

// Server is a fake upsd on 127.0.0.1.
type Server struct {
	ln net.Listener

	mu       sync.Mutex
	devices  map[string]Device
	failures map[string]string
	tlsCfg   *tls.Config
	down     bool
	conns    map[net.Conn]struct{}
	received []string
	// passwords is each PASSWORD argument as upsd's tokenizer reads it.
	passwords []string
}

// NewServer starts a fake upsd that stops when the test ends.
func NewServer(t testing.TB) *Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("nuttest: listen: %v", err)
	}
	s := &Server{
		ln:       ln,
		devices:  make(map[string]Device),
		failures: make(map[string]string),
		conns:    make(map[net.Conn]struct{}),
	}
	go s.serve()
	t.Cleanup(s.Close)
	return s
}

// Addr is where the fake listens.
func (s *Server) Addr() (host string, port int) {
	a, _ := s.ln.Addr().(*net.TCPAddr)
	return a.IP.String(), a.Port
}

// SetDevice adds or replaces a UPS.
func (s *Server) SetDevice(name string, d Device) {
	s.mu.Lock()
	defer s.mu.Unlock()
	vars := make(map[string]string, len(d.Vars))
	for k, v := range d.Vars {
		vars[k] = v
	}
	d.Vars = vars
	d.Clients = slices.Clone(d.Clients)
	s.devices[name] = d
}

// SetClients replaces the logins LIST CLIENT reports for a UPS.
func (s *Server) SetClients(ups string, addrs ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.devices[ups]; ok {
		d.Clients = slices.Clone(addrs)
		s.devices[ups] = d
	}
}

// device finds a UPS the way upsd does, ignoring case, and returns its name
// as configured.
func (s *Server) device(name string) (string, Device, bool) {
	for n, d := range s.devices {
		if strings.EqualFold(n, name) {
			return n, d, true
		}
	}
	return "", Device{}, false
}

// SetVar changes one variable of a UPS that SetDevice added.
func (s *Server) SetVar(ups, name, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.devices[ups]; ok {
		d.Vars[name] = value
	}
}

// RemoveDevice drops a UPS, as an edit of ups.conf would.
func (s *Server) RemoveDevice(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.devices, name)
}

// Fail makes the exact command line get "ERR <code>", for example
// Fail("LIST VAR ups01", "DATA-STALE"). An empty code clears it.
func (s *Server) Fail(command, code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if code == "" {
		delete(s.failures, command)
		return
	}
	s.failures[command] = code
}

// SetReachable false drops every open connection and closes new ones as soon
// as they are accepted, which a client sees as the server going away.
func (s *Server) SetReachable(up bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.down = !up
	if s.down {
		for c := range s.conns {
			_ = c.Close()
		}
	}
}

// Received returns every command line the fake has read, in order.
func (s *Server) Received() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.received...)
}

// Passwords is every PASSWORD argument received, as upsd would parse it.
func (s *Server) Passwords() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.passwords)
}

// EnableTLS makes STARTTLS succeed with a fresh self-signed certificate for
// 127.0.0.1 and returns that certificate's SHA-256 fingerprint, lowercase hex.
func (s *Server) EnableTLS(t testing.TB) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("nuttest: key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "nuttest"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("nuttest: certificate: %v", err)
	}
	s.mu.Lock()
	s.tlsCfg = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
	}
	s.mu.Unlock()
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// Close stops the fake and drops its connections.
func (s *Server) Close() {
	_ = s.ln.Close()
	s.SetReachable(false)
}

func (s *Server) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.down {
			s.mu.Unlock()
			_ = conn.Close()
			continue
		}
		s.conns[conn] = struct{}{}
		s.mu.Unlock()
		go s.handle(conn)
	}
}

func (s *Server) handle(raw net.Conn) {
	defer func() {
		s.mu.Lock()
		delete(s.conns, raw)
		s.mu.Unlock()
		_ = raw.Close()
	}()
	var rw io.ReadWriter = raw
	r := bufio.NewReader(rw)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		reply, upgrade, quit := s.answer(line)
		if _, err := io.WriteString(rw, upsdCut(reply)); err != nil || quit {
			return
		}
		if upgrade {
			s.mu.Lock()
			cfg := s.tlsCfg
			s.mu.Unlock()
			tc := tls.Server(raw, cfg)
			if err := tc.Handshake(); err != nil {
				return
			}
			rw = tc
			r = bufio.NewReader(tc)
		}
	}
}

func (s *Server) answer(line string) (reply string, startTLS, quit bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.received = append(s.received, line)
	if code, ok := s.failures[line]; ok {
		return "ERR " + code + "\n", false, false
	}
	f, ok := fields(line)
	if !ok {
		return "", false, false // upsd logs a parse error and answers nothing
	}
	if len(f) == 0 {
		return "ERR UNKNOWN-COMMAND\n", false, false
	}
	switch f[0] {
	case "VER":
		return Banner + "\n", false, false
	case "NETVER", "PROTVER":
		return "1.3\n", false, false
	case "LIST":
		return s.list(f[1:]), false, false
	case "GET":
		if len(f) == 4 && f[1] == "DESC" {
			_, d, ok := s.device(f[2])
			if !ok {
				return "ERR UNKNOWN-UPS\n", false, false
			}
			desc, ok := d.Descs[f[3]]
			if !ok {
				desc = "Description unavailable"
			}
			return "DESC " + f[2] + " " + f[3] + " " + quote(desc) + "\n", false, false
		}
		return "ERR INVALID-ARGUMENT\n", false, false
	case "STARTTLS":
		if s.tlsCfg == nil {
			return "ERR FEATURE-NOT-CONFIGURED\n", false, false
		}
		return "OK STARTTLS\n", true, false
	case "USERNAME", "PASSWORD":
		if len(f) != 2 {
			return "ERR INVALID-ARGUMENT\n", false, false
		}
		if f[0] == "PASSWORD" {
			s.passwords = append(s.passwords, f[1])
		}
		return "OK\n", false, false
	case "LOGOUT":
		return "OK Goodbye\n", false, true
	}
	return "ERR UNKNOWN-COMMAND\n", false, false
}

func (s *Server) list(args []string) string {
	var b strings.Builder
	switch {
	case len(args) == 1 && args[0] == "UPS":
		b.WriteString("BEGIN LIST UPS\n")
		for _, name := range sortedKeys(s.devices) {
			desc := s.devices[name].Description
			if desc == "" {
				desc = "Description unavailable" // upsd's text for a UPS with no desc
			}
			b.WriteString("UPS " + name + " " + quote(desc) + "\n")
		}
		b.WriteString("END LIST UPS\n")
	case len(args) == 2 && args[0] == "VAR":
		_, d, ok := s.device(args[1])
		if !ok {
			return "ERR UNKNOWN-UPS\n"
		}
		b.WriteString("BEGIN LIST VAR " + args[1] + "\n")
		for _, k := range sortedKeys(d.Vars) {
			b.WriteString("VAR " + args[1] + " " + k + " " + quote(d.Vars[k]) + "\n")
		}
		b.WriteString("END LIST VAR " + args[1] + "\n")
	case len(args) == 2 && args[0] == "CLIENT":
		name, d, ok := s.device(args[1])
		if !ok {
			return "ERR UNKNOWN-UPS\n"
		}
		// Unlike LIST VAR, the lines name the UPS as configured, not as asked.
		b.WriteString("BEGIN LIST CLIENT " + args[1] + "\n")
		for _, addr := range d.Clients {
			b.WriteString("CLIENT " + name + " " + addr + "\n")
		}
		b.WriteString("END LIST CLIENT " + args[1] + "\n")
	default:
		return "ERR INVALID-ARGUMENT\n"
	}
	return b.String()
}

// upsdCut applies upsd's reply buffer to each line: sendback() formats a line
// into 512 bytes, so a longer one goes out as its first 512, newline lost.
func upsdCut(reply string) string {
	var b strings.Builder
	for _, line := range strings.SplitAfter(reply, "\n") {
		if len(line) > 512 {
			line = line[:512]
		}
		b.WriteString(line)
	}
	return b.String()
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func quote(v string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`
}

// fields splits a request line the way upsd's tokenizer does
// (common/parseconf.c): whitespace separates words, a word starting with "
// runs to the next unescaped ", a \ takes the next byte literally, an
// unquoted = is a word of its own, an unquoted # starts a comment, and only
// bytes 0x20-0x7F are kept. ok is false where upsd answers nothing: an
// unescaped # inside quotes is a parse error, and a quote or a \ left open
// at the end of the line runs on into the next one, which the fake does not
// model. The client sends neither: quoteArg closes every quote it opens.
func fields(line string) (words []string, ok bool) {
	const (
		findStart = iota
		bare
		bareLiteral
		quoted
		quotedLiteral
	)
	var cur strings.Builder
	state := findStart
	end := func() {
		words = append(words, cur.String())
		cur.Reset()
	}
	add := func(ch byte) {
		if ch >= 0x20 && ch <= 0x7f {
			cur.WriteByte(ch)
		}
	}
	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch state {
		case findStart:
			switch {
			case ch == '#':
				return words, true
			case isSpace(ch):
			case ch == '\\':
				state = bareLiteral
			case ch == '"':
				state = quoted
			case ch == '=':
				words = append(words, "=")
			default:
				add(ch)
				state = bare
			}
		case bare:
			switch {
			case ch == '#':
				end()
				return words, true
			case isSpace(ch):
				end()
				state = findStart
			case ch == '=':
				end()
				words = append(words, "=")
				state = findStart
			case ch == '\\':
				state = bareLiteral
			default:
				add(ch)
			}
		case bareLiteral:
			add(ch)
			state = bare
		case quoted:
			switch ch {
			case '#':
				return nil, false
			case '"':
				end()
				state = findStart
			case '\\':
				state = quotedLiteral
			default:
				add(ch)
			}
		case quotedLiteral:
			add(ch)
			state = quoted
		}
	}
	switch state {
	case bare:
		end()
	case bareLiteral, quoted, quotedLiteral:
		return nil, false
	}
	return words, true
}

func isSpace(ch byte) bool {
	return ch == ' ' || ch == '\t' || ch == '\v' || ch == '\f' || ch == '\r'
}
