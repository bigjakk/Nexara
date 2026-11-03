package nut

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// commandVerbs change the UPS: INSTCMD runs an instant command, and SET sets a
// variable or switches tracking. Only a connection dialled with
// Config.Commands may send them.
var commandVerbs = map[string]bool{"INSTCMD": true, "SET": true}

const (
	// maxWordBytes is upsd's word limit (parseconf's wordlen_limit): it keeps
	// the first 512 bytes of a longer word and says nothing.
	maxWordBytes = 512
	// maxDriverValue: a driver keeps a value in ST_MAX_VALUE_LEN (256) bytes
	// with its NUL, so dstate_setinfo cuts a longer one.
	maxDriverValue = 255
	// maxForwardLine: upsd builds the line it passes to the driver in
	// SMALLBUF (512) with its NUL. snprintf drops the end of a longer one,
	// with the value's closing quote, the tracking id and the newline, and
	// the driver's parser then runs on into upsd's next line.
	maxForwardLine = 511
	// trackingSuffix is what upsd appends to that line with tracking on.
	trackingSuffix = len(" TRACKING ") + 36
)

// ErrValueChanged means upsd, or the driver it passes a command on to, would
// not get a word as sent. It is returned before anything is written.
var ErrValueChanged = errors.New("nut: upsd would not pass this on unchanged")

// checkWord refuses a word upsd's tokenizer would alter: it drops bytes
// outside 0x20–0x7F and cuts a word at 512 bytes.
func checkWord(w string) error {
	if len(w) > maxWordBytes {
		return fmt.Errorf("%w: longer than %d bytes", ErrValueChanged, maxWordBytes)
	}
	for i := 0; i < len(w); i++ {
		if w[i] < 0x20 || w[i] > 0x7f {
			return fmt.Errorf("%w: byte 0x%02x", ErrValueChanged, w[i])
		}
	}
	return nil
}

// CheckCommand refuses an instant command its driver would not get as asked.
// upsd passes the parameter on unquoted (netinstcmd.c), so the driver would
// split one holding a space or an "=" into words: a 2.8 driver drops the
// command, leaving its tracking pending for an hour, and a 2.7 one runs it
// with the first word.
func CheckCommand(cmd, param string) error {
	if err := checkWord(cmd); err != nil {
		return err
	}
	if err := checkWord(param); err != nil {
		return err
	}
	if strings.ContainsAny(param, " =") {
		return fmt.Errorf("%w: upsd passes a parameter on unquoted, so a space or an = would split it", ErrValueChanged)
	}
	line := len("INSTCMD ") + len(cmd) + trackingSuffix + len("\n")
	if param != "" {
		line += 1 + encodedLen(param)
	}
	if line > maxForwardLine {
		return fmt.Errorf("%w: upsd's line to the driver would pass 512 bytes", ErrValueChanged)
	}
	return nil
}

// CheckSetValue refuses a variable setting its driver would not get as asked:
// a value past the driver's 255 bytes, or a line upsd's buffer would cut
// (netset.c quotes the value, escaping each #, \ and ").
func CheckSetValue(name, value string) error {
	if err := checkWord(name); err != nil {
		return err
	}
	if err := checkWord(value); err != nil {
		return err
	}
	if len(value) > maxDriverValue {
		return fmt.Errorf("%w: a driver keeps %d bytes of a value", ErrValueChanged, maxDriverValue)
	}
	if len("SET ")+len(name)+len(` ""`)+encodedLen(value)+trackingSuffix+len("\n") > maxForwardLine {
		return fmt.Errorf("%w: upsd's line to the driver would pass 512 bytes", ErrValueChanged)
	}
	return nil
}

// encodedLen is a word's length once pconf_encode has escaped each #, \ and ".
func encodedLen(w string) int {
	return len(w) + strings.Count(w, "#") + strings.Count(w, `\`) + strings.Count(w, `"`)
}

// ListCommands returns the instant commands a UPS supports.
func (c *Client) ListCommands(ctx context.Context, ups string) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rows, err := c.list(ctx, "CMD", ups)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if len(r) != 3 || r[0] != "CMD" || r[1] != ups {
			return nil, fmt.Errorf("nut: unexpected LIST CMD line %q", clip(strings.Join(r, " "), 64))
		}
		out = append(out, r[2])
	}
	return out, nil
}

// CommandDescription returns upsd's description of an instant command, "" when
// it has none or its reply arrived cut.
func (c *Client) CommandDescription(ctx context.Context, ups, cmd string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.describe(ctx, "CMDDESC", ups, cmd)
}

// GetVar returns one variable's value, as a read-back after SetVar. A reply
// past upsd's 512-byte buffer arrives cut and is an error.
func (c *Client) GetVar(ctx context.Context, ups, name string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	line, err := c.roundTrip(ctx, "GET", "VAR", ups, name)
	if err != nil {
		return "", err
	}
	toks, err := splitLine(line)
	if err != nil {
		return "", err
	}
	if len(toks) != 4 || toks[0] != "VAR" {
		return "", fmt.Errorf("nut: unexpected GET VAR reply %q", clip(line, 64))
	}
	return toks[3], nil
}

// ListRW returns the variables of a UPS a client may set, with their values.
// It, not GET TYPE, says what is writable: 2.7.4 leaves RW off numbers there.
func (c *Client) ListRW(ctx context.Context, ups string) (map[string]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.listValues(ctx, "RW", ups)
}

// VarType is what GET TYPE says of a variable.
type VarType struct {
	Writable bool
	// Enum and Range: a value must be one LIST ENUM lists, or within a pair
	// LIST RANGE lists.
	Enum, Range bool
	// MaxLen is STRING:n's n, 0 when upsd did not say STRING.
	MaxLen int
	Number bool
}

// VarType returns a variable's type: RW, ENUM and RANGE where they apply,
// then STRING:n or NUMBER (netget.c's get_type, 2.8.1; 2.7.4 stops after
// ENUM or RANGE). A word this client does not know is ignored.
func (c *Client) VarType(ctx context.Context, ups, name string) (VarType, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	line, err := c.roundTrip(ctx, "GET", "TYPE", ups, name)
	if err != nil {
		return VarType{}, err
	}
	toks, err := splitLine(line)
	if err != nil {
		return VarType{}, err
	}
	if len(toks) < 3 || toks[0] != "TYPE" {
		return VarType{}, fmt.Errorf("nut: unexpected GET TYPE reply %q", clip(line, 64))
	}
	var t VarType
	for _, w := range toks[3:] {
		switch {
		case w == "RW":
			t.Writable = true
		case w == "ENUM":
			t.Enum = true
		case w == "RANGE":
			t.Range = true
		case w == "NUMBER":
			t.Number = true
		case strings.HasPrefix(w, "STRING:"):
			n, err := strconv.Atoi(strings.TrimPrefix(w, "STRING:"))
			if err != nil || n < 0 {
				return VarType{}, fmt.Errorf("nut: unexpected GET TYPE reply %q", clip(line, 64))
			}
			t.MaxLen = n
		}
	}
	return t, nil
}

// ListEnum returns the values a variable may be set to, empty when upsd lists
// none.
func (c *Client) ListEnum(ctx context.Context, ups, name string) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rows, err := c.list(ctx, "ENUM", ups, name)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if len(r) != 4 || r[0] != "ENUM" || r[1] != ups || r[2] != name {
			return nil, fmt.Errorf("nut: unexpected LIST ENUM line %q", clip(strings.Join(r, " "), 64))
		}
		out = append(out, r[3])
	}
	return out, nil
}

// Range is one LIST RANGE pair. upsd checks a value against it as atoi does,
// so "12.7" counts as 12.
type Range struct {
	Min, Max int
}

// ListRange returns the ranges a variable's value must fall in, empty when
// upsd lists none.
func (c *Client) ListRange(ctx context.Context, ups, name string) ([]Range, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rows, err := c.list(ctx, "RANGE", ups, name)
	if err != nil {
		return nil, err
	}
	out := make([]Range, 0, len(rows))
	for _, r := range rows {
		if len(r) != 5 || r[0] != "RANGE" || r[1] != ups || r[2] != name {
			return nil, fmt.Errorf("nut: unexpected LIST RANGE line %q", clip(strings.Join(r, " "), 64))
		}
		lo, err1 := strconv.Atoi(r[3])
		hi, err2 := strconv.Atoi(r[4])
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("nut: unexpected LIST RANGE line %q", clip(strings.Join(r, " "), 64))
		}
		out = append(out, Range{Min: lo, Max: hi})
	}
	return out, nil
}

// SetTracking switches command tracking for this connection. It needs
// protocol 1.3 (NUT 2.8): a 2.7 upsd answers ERR INVALID-ARGUMENT. upsd wants
// a username and password sent first, but does not check them here.
func (c *Client) SetTracking(ctx context.Context, on bool) error {
	arg := "OFF"
	if on {
		arg = "ON"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	line, err := c.roundTrip(ctx, "SET", "TRACKING", arg)
	if err != nil {
		return err
	}
	if line != "OK" {
		return fmt.Errorf("nut: unexpected SET TRACKING reply %q", clip(line, 64))
	}
	return nil
}

// InstantCommand runs an instant command, with arg as its parameter unless it
// is "". Success only means upsd handed it to the driver: with tracking on,
// Tracking reports the driver's outcome for the returned id, which is ""
// with tracking off.
func (c *Client) InstantCommand(ctx context.Context, ups, cmd, arg string) (string, error) {
	if err := CheckCommand(cmd, arg); err != nil {
		return "", err
	}
	if arg == "" {
		return c.change(ctx, "INSTCMD", ups, cmd)
	}
	return c.change(ctx, "INSTCMD", ups, cmd, arg)
}

// SetVar sets a variable, answering as InstantCommand does. upsd checks the
// value against the variable's ENUM, RANGE or STRING:n; a number it passes
// on unchecked, and only tracking shows whether the driver took it.
func (c *Client) SetVar(ctx context.Context, ups, name, value string) (string, error) {
	if err := CheckSetValue(name, value); err != nil {
		return "", err
	}
	return c.change(ctx, "SET", "VAR", ups, name, value)
}

// change sends a command that changes the UPS, after its caller has checked
// what is passed on to the driver.
func (c *Client) change(ctx context.Context, verb string, args ...string) (string, error) {
	for _, a := range args {
		if err := checkWord(a); err != nil {
			return "", err
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	line, err := c.roundTrip(ctx, verb, args...)
	if err != nil {
		return "", err
	}
	if line == "OK" {
		return "", nil
	}
	if id, ok := strings.CutPrefix(line, "OK TRACKING "); ok && id != "" && len(id) <= 64 && !strings.ContainsAny(id, " \t") {
		return id, nil
	}
	return "", fmt.Errorf("nut: unexpected %s reply %q", verb, clip(line, 64))
}

// TrackingStatus is what GET TRACKING reports of one tracked command.
type TrackingStatus string

const (
	TrackingPending TrackingStatus = "pending"
	TrackingSuccess TrackingStatus = "success"
	// TrackingUnknown is the driver's own unknown error, and also what upsd
	// says of an id it does not hold: one that expired (TRACKINGDELAY, an
	// hour by default) or one issued before a restart.
	TrackingUnknown TrackingStatus = "unknown"
	// TrackingInvalid means the driver refused the command's parameter or the
	// variable's value.
	TrackingInvalid TrackingStatus = "invalid_argument"
	TrackingFailed  TrackingStatus = "failed"
)

// Tracking returns the outcome of a command run with tracking on. Its ERR
// answers are outcomes, not errors; ERR FEATURE-NOT-CONFIGURED, for a
// connection with tracking off, is the error. Ids repeat across upsd
// restarts, so ask on the connection that ran the command.
func (c *Client) Tracking(ctx context.Context, id string) (TrackingStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	line, err := c.roundTrip(ctx, "GET", "TRACKING", id)
	switch errCode(err) {
	case "UNKNOWN":
		return TrackingUnknown, nil
	case "INVALID-ARGUMENT":
		return TrackingInvalid, nil
	case "FAILED":
		return TrackingFailed, nil
	}
	if err != nil {
		return "", err
	}
	switch line {
	case "PENDING":
		return TrackingPending, nil
	case "SUCCESS":
		return TrackingSuccess, nil
	}
	return "", fmt.Errorf("nut: unexpected GET TRACKING reply %q", clip(line, 64))
}
