package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// TestSendErrorsKeepNoChannelSecret holds a failed send's error, which a
// dead-letter row every Viewer can read keeps, to its channel's scheme and host:
// a chat webhook's path and token query are its secret. Each sender's own
// request path, and the URL check both share, fail here without a network.
func TestSendErrorsKeepNoChannelSecret(t *testing.T) {
	t.Parallel()
	const probe = "PROBE-WEBHOOK-SECRET-NOT-A-REAL-VALUE"
	hook := "https://192.0.2.1/services/T000/B000/" + probe + "?token=" + probe
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tt := range []struct {
		name       string
		dispatcher Dispatcher
		ctx        context.Context
		config     map[string]string
	}{
		{"a chat webhook's send", &SlackDispatcher{}, canceled, map[string]string{"webhook_url": hook}},
		{"a webhook's send", &WebhookDispatcher{}, canceled, map[string]string{"url": hook}},
		{"a URL that does not parse", &DiscordDispatcher{}, context.Background(), map[string]string{"webhook_url": hook + "\x7f"}},
	} {
		config, _ := json.Marshal(tt.config)
		err := tt.dispatcher.Send(tt.ctx, config, AlertPayload{RuleName: "rule01", Severity: "warning"})
		if err == nil || strings.Contains(err.Error(), probe) || !strings.Contains(err.Error(), "https://192.0.2.1/(set)") {
			t.Errorf("%s: error %v, want one naming the channel as https://192.0.2.1/(set)", tt.name, err)
		}
	}
}

// TestCheckTargetIP is the address rule every send applies: a channel's
// confirmation relaxes the private ranges alone, and the carrier-grade NAT a
// channel from before v1.18.0 keeps relaxes that range alone; loopback,
// link-local (the metadata addresses) and unspecified stay refused whatever
// either says.
func TestCheckTargetIP(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		ip                            string
		without, withAllow, withCGNAT bool // refused
		overridableIfRefused          bool
	}{
		{"10.0.0.5", true, false, true, true},
		{"172.16.0.9", true, false, true, true},
		{"fd00::5", true, false, true, true},
		{"127.0.0.1", true, true, true, false},
		{"::1", true, true, true, false},
		{"169.254.169.254", true, true, true, false},
		{"fe80::1", true, true, true, false},
		{"0.0.0.0", true, true, true, false},
		{"::", true, true, true, false},
		{"100.64.0.9", true, false, false, true},
		{"100.100.100.200", true, true, true, false}, // Alibaba Cloud's metadata service, inside CGNAT
		{"fd00:ec2::254", true, true, true, false},
		{"fd20:ce::254", true, true, true, false},
		{"203.0.113.10", false, false, false, false},
	} {
		ip := net.ParseIP(tt.ip)
		errWithout, errWith := checkTargetIP(ip, Allowance{}), checkTargetIP(ip, Allowance{Private: true})
		errCGNAT := checkTargetIP(ip, Allowance{CGNAT: true})
		if (errWithout != nil) != tt.without || (errWith != nil) != tt.withAllow || (errCGNAT != nil) != tt.withCGNAT {
			t.Errorf("%s: refused %v without, %v with the confirmation and %v with CGNAT alone, want %v, %v and %v",
				tt.ip, errWithout, errWith, errCGNAT, tt.without, tt.withAllow, tt.withCGNAT)
		}
		var te *TargetAddressError
		if errors.As(errWithout, &te) && te.Overridable != tt.overridableIfRefused {
			t.Errorf("%s: overridable %v, want %v", tt.ip, te.Overridable, tt.overridableIfRefused)
		}
	}
}

// The send-time checks — a URL's resolution, the dialled address, an SMTP
// host's — each read the allowance the channel's context carries.
func TestSendTimeChecksReadTheChannelsAllowance(t *testing.T) {
	t.Parallel()
	allowed := ChannelContext(context.Background(), db.NotificationChannel{AllowPrivateAddress: true})
	grandfathered := ChannelContext(context.Background(), db.NotificationChannel{CgnatGrandfathered: true})
	refused := ChannelContext(context.Background(), db.NotificationChannel{})
	// smtpSend goes through SendSMTPMessage with ctx cancelled, so a send whose
	// address passes stops at the dial and contacts nothing: only a refused
	// address comes back as a refusal.
	smtpSend := func(host string) func(ctx context.Context) error {
		return func(ctx context.Context) error {
			c, cancel := context.WithCancel(ctx)
			cancel()
			var refusal *TargetAddressError
			err := SendSMTPMessage(c, SMTPSendOptions{Host: host, To: []string{"ops@example.com"}, UseSTARTTLS: true})
			if errors.As(err, &refusal) {
				return err
			}
			return nil
		}
	}
	for _, tt := range []struct {
		name                               string
		check                              func(ctx context.Context) error
		allowedPasses, grandfatheredPasses bool
	}{
		{"private URL", func(ctx context.Context) error { return validateExternalURL(ctx, "https://10.0.0.5/hook") }, true, false},
		{"CGNAT URL", func(ctx context.Context) error { return validateExternalURL(ctx, "https://100.64.0.9/hook") }, true, true},
		{"loopback URL", func(ctx context.Context) error { return validateExternalURL(ctx, "https://127.0.0.1/hook") }, false, false},
		{"private dial", func(ctx context.Context) error {
			return dialControl(allowanceFrom(ctx))("tcp", "10.0.0.5:443", nil)
		}, true, false},
		{"CGNAT dial", func(ctx context.Context) error {
			return dialControl(allowanceFrom(ctx))("tcp", "100.64.0.9:443", nil)
		}, true, true},
		{"metadata dial", func(ctx context.Context) error {
			return dialControl(allowanceFrom(ctx))("tcp", "169.254.169.254:80", nil)
		}, false, false},
		{"zoned loopback dial", func(ctx context.Context) error {
			return dialControl(allowanceFrom(ctx))("tcp", "[::1%1]:443", nil)
		}, false, false},
		{"private SMTP send", smtpSend("10.0.0.5"), true, false},
		{"CGNAT SMTP send", smtpSend("100.64.0.9"), true, true},
	} {
		if err := tt.check(refused); err == nil {
			t.Errorf("%s: passed on a channel never allowed", tt.name)
		}
		if err := tt.check(allowed); (err == nil) != tt.allowedPasses {
			t.Errorf("%s on an allowed channel: %v, want passing %v", tt.name, err, tt.allowedPasses)
		}
		if err := tt.check(grandfathered); (err == nil) != tt.grandfatheredPasses {
			t.Errorf("%s on a channel from before v1.18.0: %v, want passing %v", tt.name, err, tt.grandfatheredPasses)
		}
	}
}

// TestCheckChannelTarget: what a channel's settings send to is checked before
// they are stored, by channel type.
func TestCheckChannelTarget(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		typ, config string
		allow       bool
		private     bool
		refused     string // "", "overridable", "hard"
	}{
		{"webhook", `{"url":"https://10.0.0.5/hook"}`, false, true, "overridable"},
		{"webhook", `{"url":"https://10.0.0.5/hook"}`, true, true, ""},
		{"slack", `{"webhook_url":"https://127.0.0.1/x"}`, true, false, "hard"},
		{"email", `{"host":"10.0.0.5"}`, false, true, "overridable"},
		{"email", `{"host":"10.0.0.5","tls":false}`, false, false, ""},
		{"webhook", `{"url":"https://203.0.113.10/hook"}`, true, false, ""},
		{"telegram", `{"bot_token":"1:x","chat_id":"5"}`, false, false, ""},
	} {
		private, err := CheckChannelTarget(context.Background(), tt.typ, json.RawMessage(tt.config), tt.allow)
		refused := ""
		var te *TargetAddressError
		if errors.As(err, &te) {
			refused = map[bool]string{true: "overridable", false: "hard"}[te.Overridable]
		}
		if private != tt.private || refused != tt.refused || (err != nil) != (refused != "") {
			t.Errorf("%s %s allow=%v: private %v, err %v; want private %v, refused %q",
				tt.typ, tt.config, tt.allow, private, err, tt.private, tt.refused)
		}
	}
}

// An IPv6 zone (%1) makes net.ParseIP fail while the resolver and the dialer
// accept the address, so a check that skips what it cannot parse fails open.
// Loopback and the metadata address, zoned, are refused before any connect.
func TestZonedAddressesAreRefused(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback listener:", err)
	}
	defer ln.Close()
	accepted := make(chan struct{}, 8)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- struct{}{}
			_ = c.Close()
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	allowed := ChannelContext(context.Background(), db.NotificationChannel{AllowPrivateAddress: true})
	for _, u := range []string{
		"http://[::ffff:127.0.0.1%251]:" + port + "/",
		"http://[::1%251]:" + port + "/",
		"http://[::ffff:169.254.169.254%251]/",
	} {
		config, _ := json.Marshal(map[string]string{"url": u})
		err := (&WebhookDispatcher{}).Send(allowed, config, AlertPayload{})
		var te *TargetAddressError
		if !errors.As(err, &te) {
			t.Errorf("%s: %v, want a refused target", u, err)
		}
		if _, err := CheckChannelTarget(context.Background(), "webhook", config, true); err == nil {
			t.Errorf("%s: passed the channel check", u)
		}
	}
	select {
	case <-accepted:
		t.Error("a zoned loopback URL reached the listener")
	default:
	}
}
