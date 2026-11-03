// Package nut is a read-only client for upsd, the Network UPS Tools network
// server (TCP 3493).
//
// Read-only is enforced, not assumed. Every command goes through Client.write,
// which refuses any verb outside allowedVerbs, and any argument holding a line
// break, before a byte is written. In particular the client can never send
// LOGIN, PRIMARY/MASTER or FSD. LOGIN registers the caller as a upsmon
// secondary, and the primary upsmon waits for the number of attached
// secondaries to fall to itself before it powers the UPS off: a Nexara that
// logged in would delay the shutdown every node relies on.
//
// The wire format was checked against upsd 2.8.1; the fidelity table in
// nuttest records what that server answered.
package nut

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bigjakk/nexara/internal/netguard"
)

// DefaultPort is upsd's IANA-assigned port.
const DefaultPort = 3493

const (
	defaultTimeout = 5 * time.Second
	// maxLineBytes and maxListLines bound what one reply may cost. upsd output
	// is untrusted text: a broken or hostile server must not balloon a stored
	// row or wedge a poll. A large ePDU lists a few hundred variables.
	maxLineBytes = 4096
	maxListLines = 4096
	// upsdReplyCap is upsd's reply buffer: sendback() formats each reply line
	// into 512 bytes, so a longer one arrives cut to exactly that, without its
	// newline, and nothing follows until the next command.
	upsdReplyCap = 512
	// maxVersionLen caps the release string kept from VER's banner.
	maxVersionLen = 32
)

// TLSMode selects whether the connection is upgraded with STARTTLS.
type TLSMode string

const (
	TLSOff      TLSMode = "off"
	TLSStartTLS TLSMode = "starttls"
)

// Config describes one upsd connection.
type Config struct {
	Host string
	// Port is the upsd port; 0 means DefaultPort.
	Port int
	// Username and Password are sent with USERNAME/PASSWORD when set. upsd
	// needs them for privileged commands, which this client never sends, but
	// a server may also demand them before it answers reads.
	Username string
	Password string
	TLS      TLSMode
	// VerifyTLS and Fingerprint follow internal/veeam's precedence: a pinned
	// leaf fingerprint wins, then VerifyTLS=false skips verification, and
	// otherwise the system CA pool verifies the chain.
	VerifyTLS   bool
	Fingerprint string
	// Timeout bounds the dial and each command; 0 means 5 s.
	Timeout time.Duration
}

// UPS is one entry of LIST UPS.
type UPS struct {
	Name        string
	Description string
}

// Client is one upsd connection. Its methods serialise on a mutex, so one
// command runs at a time. After an I/O error the connection's framing is
// unknown, so the Client refuses further commands and the caller redials.
type Client struct {
	mu          sync.Mutex
	conn        net.Conn
	r           *bufio.Reader
	timeout     time.Duration
	broken      error
	fingerprint string
}

// allowedVerbs is the whole of what this client may say. Every GET and LIST
// subcommand is a read. LOGIN, PRIMARY, MASTER and FSD are absent for the
// reason in the package comment; INSTCMD and SET would change the UPS.
var allowedVerbs = map[string]bool{
	"VER":      true,
	"NETVER":   true,
	"PROTVER":  true,
	"LIST":     true,
	"GET":      true,
	"STARTTLS": true,
	"USERNAME": true,
	"PASSWORD": true,
	"LOGOUT":   true,
}

var (
	// ErrVerbNotAllowed is returned, before anything is written, for a verb
	// outside allowedVerbs.
	ErrVerbNotAllowed = errors.New("nut: command not allowed for a read-only client")
	// ErrTLSUnavailable means STARTTLS was required and the server refused it.
	// The client never falls back to plain text on its own.
	ErrTLSUnavailable = errors.New("nut: server does not offer STARTTLS")

	errLineTooLong = errors.New("nut: reply line longer than 4 KiB")
	errListTooLong = errors.New("nut: list reply longer than 4096 lines")
	// errReplyCut is a one-line reply upsd cut at upsdReplyCap. The text past
	// the cut is lost, but the connection is still in step.
	errReplyCut = errors.New("nut: reply cut at upsd's 512-byte limit")
)

// Dial connects to upsd, upgrades to TLS when cfg asks for it, and sends the
// credentials if any are set.
func Dial(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Host == "" {
		return nil, errors.New("nut: host is empty")
	}
	if cfg.TLS != "" && cfg.TLS != TLSOff && cfg.TLS != TLSStartTLS {
		return nil, fmt.Errorf("nut: unknown TLS mode %q", cfg.TLS)
	}
	port := cfg.Port
	if port == 0 {
		port = DefaultPort
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(port))
	// The SSRF guard re-checks the resolved address at dial time, after the
	// handler's address policy, so a DNS rebind cannot reach a blocked range.
	dialer := net.Dialer{Timeout: timeout, Control: netguard.DialControlSSRFGuard}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("nut: dial %s: %w", addr, err)
	}
	c := &Client{conn: conn, r: bufio.NewReaderSize(conn, maxLineBytes), timeout: timeout}
	if err := c.setup(ctx, cfg); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) setup(ctx context.Context, cfg Config) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cfg.TLS == TLSStartTLS {
		if err := c.startTLS(ctx, cfg); err != nil {
			return err
		}
	}
	if cfg.Username != "" {
		if _, err := c.roundTrip(ctx, "USERNAME", cfg.Username); err != nil {
			return err
		}
	}
	if cfg.Password != "" {
		if _, err := c.roundTrip(ctx, "PASSWORD", cfg.Password); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) startTLS(ctx context.Context, cfg Config) error {
	line, err := c.roundTrip(ctx, "STARTTLS")
	if err != nil {
		if code := errCode(err); code == "FEATURE-NOT-CONFIGURED" || code == "FEATURE-NOT-SUPPORTED" {
			return fmt.Errorf("%w (%s)", ErrTLSUnavailable, code)
		}
		return err
	}
	if !strings.HasPrefix(line, "OK STARTTLS") {
		return fmt.Errorf("nut: unexpected STARTTLS reply %q", clip(line, 64))
	}
	// Anything already buffered arrived in plain text after the server agreed
	// to upgrade. Reading it later as if it came over TLS is the classic
	// STARTTLS injection, so refuse the connection instead.
	if c.r.Buffered() != 0 {
		return errors.New("nut: server sent data after agreeing to STARTTLS")
	}
	tc := tlsConfig(cfg)
	// Record the leaf in every mode so a probe can offer it for pinning. No
	// session is ever resumed (each dial builds a fresh config), and turning
	// tickets off keeps it that way.
	tc.SessionTicketsDisabled = true
	var seen string
	verify := tc.VerifyPeerCertificate
	tc.VerifyPeerCertificate = func(raw [][]byte, chains [][]*x509.Certificate) error {
		if len(raw) > 0 {
			seen = Fingerprint(raw[0])
		}
		if verify != nil {
			return verify(raw, chains)
		}
		return nil
	}
	tlsConn := tls.Client(c.conn, tc)
	hsCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if err := tlsConn.HandshakeContext(hsCtx); err != nil {
		return fmt.Errorf("nut: TLS handshake: %w", err)
	}
	c.conn = tlsConn
	c.r = bufio.NewReaderSize(tlsConn, maxLineBytes)
	c.fingerprint = seen
	return nil
}

func tlsConfig(cfg Config) *tls.Config {
	tc := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: cfg.Host}
	switch {
	case cfg.Fingerprint != "":
		want := NormalizeFingerprint(cfg.Fingerprint)
		tc.InsecureSkipVerify = true //nolint:gosec // VerifyPeerCertificate below pins the leaf fingerprint
		// A resumed session would skip VerifyPeerCertificate, so the pin has to
		// turn tickets off (gosec G123).
		tc.SessionTicketsDisabled = true
		tc.VerifyPeerCertificate = func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("nut: server presented no certificate")
			}
			if got := Fingerprint(raw[0]); got != want {
				return fmt.Errorf("nut: TLS fingerprint mismatch: got %s, want %s", got, want)
			}
			return nil
		}
	case !cfg.VerifyTLS:
		tc.InsecureSkipVerify = true //nolint:gosec // G402: explicit operator opt-out, acknowledged in the UI and audit log
	}
	return tc
}

// Fingerprint returns the lowercase hex SHA-256 digest of a DER certificate.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// NormalizeFingerprint reduces a fingerprint to bare lowercase hex, accepting
// the colon-separated uppercase form the UI shows.
func NormalizeFingerprint(s string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), ":", ""))
}

// RemoteAddr is the address the connection reached, so a caller can tell which
// host upsd runs on; the zero Addr if it is not a TCP connection.
func (c *Client) RemoteAddr() netip.Addr {
	c.mu.Lock()
	defer c.mu.Unlock()
	if a, ok := c.conn.RemoteAddr().(*net.TCPAddr); ok {
		return a.AddrPort().Addr().Unmap()
	}
	return netip.Addr{}
}

// PeerFingerprint is the leaf certificate's fingerprint after STARTTLS, or ""
// on a plain connection, so a probe can offer it for pinning.
func (c *Client) PeerFingerprint() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fingerprint
}

// Versions returns the NUT release from VER's banner and the protocol version
// from NETVER. Either is "" when the server does not say: VER is documented as
// a banner for humans, and NETVER only exists from protocol 1.2.
func (c *Client) Versions(ctx context.Context) (release, protocol string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	banner, err := c.roundTrip(ctx, "VER")
	switch {
	case err == nil:
		release = parseRelease(banner)
	case errCode(err) == "":
		return "", "", err
	}
	protocol, err = c.roundTrip(ctx, "NETVER")
	switch {
	case err == nil:
		protocol = clip(strings.TrimSpace(protocol), maxVersionLen)
	case errCode(err) == "":
		return "", "", err
	}
	return release, protocol, nil
}

// parseRelease takes the word after "upsd" from a banner such as
// "Network UPS Tools upsd 2.8.1 - https://www.networkupstools.org/".
func parseRelease(banner string) string {
	f := strings.Fields(banner)
	for i := 0; i+1 < len(f); i++ {
		if f[i] == "upsd" {
			return clip(f[i+1], maxVersionLen)
		}
	}
	return ""
}

// ListUPS returns every UPS the server is configured for.
func (c *Client) ListUPS(ctx context.Context) ([]UPS, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rows, err := c.list(ctx, "UPS")
	if err != nil {
		return nil, err
	}
	out := make([]UPS, 0, len(rows))
	for _, r := range rows {
		if len(r) < 3 || r[0] != "UPS" {
			return nil, fmt.Errorf("nut: unexpected LIST UPS line %q", clip(strings.Join(r, " "), 64))
		}
		desc := r[2]
		if desc == noDescription {
			desc = "" // what upsd sends for a UPS with no desc in ups.conf
		}
		out = append(out, UPS{Name: r[1], Description: desc})
	}
	return out, nil
}

// ListVars returns every variable of one UPS. A stale driver answers with an
// ERR the caller can test with IsStale.
func (c *Client) ListVars(ctx context.Context, ups string) (map[string]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rows, err := c.list(ctx, "VAR", ups)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		if len(r) < 4 || r[0] != "VAR" || r[1] != ups {
			return nil, fmt.Errorf("nut: unexpected LIST VAR line %q", clip(strings.Join(r, " "), 64))
		}
		out[r[2]] = r[3]
	}
	return out, nil
}

// ListClients returns the address of every connection logged in to a UPS,
// which is how upsmon attaches, primary and secondary alike. upsd asks no
// credentials for it and answers while the driver is down. Two connections
// from one address (NAT, or two upsmons on one host) are two entries, and an
// IPv4 address upsd shows IPv6-mapped comes back as IPv4.
func (c *Client) ListClients(ctx context.Context, ups string) ([]netip.Addr, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rows, err := c.list(ctx, "CLIENT", ups)
	if err != nil {
		return nil, err
	}
	out := make([]netip.Addr, 0, len(rows))
	for _, r := range rows {
		// upsd finds the UPS case-insensitively and names it its own way here,
		// unlike LIST VAR, which echoes the name as asked.
		if len(r) != 3 || r[0] != "CLIENT" || !strings.EqualFold(r[1], ups) {
			return nil, fmt.Errorf("nut: unexpected LIST CLIENT line %q", clip(strings.Join(r, " "), 64))
		}
		addr, err := netip.ParseAddr(r[2])
		if err != nil {
			return nil, fmt.Errorf("nut: unexpected LIST CLIENT address %q", clip(r[2], 64))
		}
		out = append(out, addr.Unmap())
	}
	return out, nil
}

// noDescription is upsd 2.8.1's placeholder: GET DESC answers it for a
// variable it has no text for, instead of an ERR, and LIST UPS for a UPS with
// no desc in ups.conf.
const noDescription = "Description unavailable"

// VarDescription returns upsd's description of a variable, "" when it has none
// or its reply did not fit upsd's buffer and arrived cut.
func (c *Client) VarDescription(ctx context.Context, ups, name string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	line, err := c.roundTrip(ctx, "GET", "DESC", ups, name)
	if errors.Is(err, errReplyCut) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	toks, err := splitLine(line)
	if err != nil {
		return "", err
	}
	if len(toks) < 4 || toks[0] != "DESC" {
		return "", fmt.Errorf("nut: unexpected GET DESC reply %q", clip(line, 64))
	}
	if toks[3] == noDescription {
		return "", nil
	}
	return toks[3], nil
}

// Close says LOGOUT, best effort, and closes the connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.broken == nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, _ = c.roundTrip(ctx, "LOGOUT")
		cancel()
	}
	c.broken = net.ErrClosed
	return c.conn.Close()
}

// roundTrip sends one command and returns its single-line reply. An ERR reply
// is a *ProtocolError and leaves the connection usable.
func (c *Client) roundTrip(ctx context.Context, verb string, args ...string) (string, error) {
	disarm, err := c.arm(ctx)
	if err != nil {
		return "", err
	}
	defer disarm()
	if err := c.write(ctx, verb, args...); err != nil {
		return "", err
	}
	line, err := c.readReply()
	if errors.Is(err, errReplyCut) {
		return "", err // nothing follows a cut reply, so the connection stays usable
	}
	if err != nil {
		return "", c.fail(ctx, verb, err)
	}
	if pe := parseErr(line); pe != nil {
		return "", pe
	}
	return line, nil
}

// list sends LIST <args> and returns the tokenised lines between BEGIN LIST
// and END LIST.
func (c *Client) list(ctx context.Context, args ...string) ([][]string, error) {
	disarm, err := c.arm(ctx)
	if err != nil {
		return nil, err
	}
	defer disarm()
	if err := c.write(ctx, "LIST", args...); err != nil {
		return nil, err
	}
	first, err := c.readLine()
	if err != nil {
		return nil, c.fail(ctx, "LIST", err)
	}
	if pe := parseErr(first); pe != nil {
		return nil, pe
	}
	query := append([]string{"LIST"}, args...)
	if toks, err := splitLine(first); err != nil || !framedBy(toks, "BEGIN", query) {
		return nil, c.fail(ctx, "LIST", fmt.Errorf("unexpected first line %q", clip(first, 64)))
	}
	var rows [][]string
	for {
		if len(rows) >= maxListLines {
			return nil, c.fail(ctx, "LIST", errListTooLong)
		}
		line, err := c.readLine()
		if err != nil {
			return nil, c.fail(ctx, "LIST", err)
		}
		toks, err := splitLine(line)
		if err != nil {
			return nil, c.fail(ctx, "LIST", err)
		}
		if framedBy(toks, "END", query) {
			return rows, nil
		}
		rows = append(rows, toks)
	}
}

func framedBy(toks []string, marker string, query []string) bool {
	if len(toks) != len(query)+1 || toks[0] != marker {
		return false
	}
	for i, q := range query {
		if toks[i+1] != q {
			return false
		}
	}
	return true
}

// arm bounds one command by the client timeout and by ctx, and refuses to
// start on a connection an earlier error left out of step.
func (c *Client) arm(ctx context.Context) (disarm func(), err error) {
	if c.broken != nil {
		return nil, fmt.Errorf("nut: connection unusable: %w", c.broken)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(c.timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = c.conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = c.conn.SetDeadline(time.Now()) })
	return func() {
		stop()
		_ = c.conn.SetDeadline(time.Time{})
	}, nil
}

// fail marks the connection unusable: after a read or framing error the next
// reply could belong to the wrong command. A command cut short by ctx reports
// ctx's error, not the i/o timeout the deadline produced, so a caller can tell
// "we stopped looking" from "the server did not answer".
func (c *Client) fail(ctx context.Context, verb string, err error) error {
	if cerr := ctx.Err(); cerr != nil {
		err = cerr
	}
	c.broken = err
	return fmt.Errorf("nut: %s: %w", verb, err)
}

// write is the only path to the wire.
func (c *Client) write(ctx context.Context, verb string, args ...string) error {
	if !allowedVerbs[verb] {
		return fmt.Errorf("%w: %s", ErrVerbNotAllowed, verb)
	}
	var b strings.Builder
	b.WriteString(verb)
	for _, a := range args {
		// A line break would end this command and start another, which is how
		// a crafted UPS name or password could smuggle in a LOGIN.
		if strings.ContainsAny(a, "\r\n\x00") {
			return fmt.Errorf("nut: %s argument holds a line break or NUL", verb)
		}
		b.WriteByte(' ')
		b.WriteString(quoteArg(a))
	}
	b.WriteByte('\n')
	if _, err := io.WriteString(c.conn, b.String()); err != nil {
		return c.fail(ctx, verb, err)
	}
	return nil
}

func (c *Client) readLine() (string, error) {
	line, err := c.r.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return "", errLineTooLong
	}
	if err != nil {
		return "", err
	}
	return cleanLine(line)
}

// readReply reads a one-line reply. Waiting for a newline upsd cut off would
// stall until the deadline and break the connection, so it stops at
// upsdReplyCap bytes: a complete reply, newline included, always fits.
func (c *Client) readReply() (string, error) {
	line := make([]byte, 0, 64)
	for len(line) < upsdReplyCap {
		b, err := c.r.ReadByte()
		if err != nil {
			return "", err
		}
		if b == '\n' {
			return cleanLine(line)
		}
		line = append(line, b)
	}
	return "", errReplyCut
}

func cleanLine(line []byte) (string, error) {
	s := strings.TrimRight(string(line), "\r\n")
	if strings.IndexByte(s, 0) >= 0 {
		return "", errors.New("reply holds a NUL byte")
	}
	return s, nil
}

// clip shortens s to at most n bytes for an error message or a stored field.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
