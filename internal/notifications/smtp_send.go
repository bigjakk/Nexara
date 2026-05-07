package notifications

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"time"
)

// SMTPSendOptions configures a single SMTP transmission via SendSMTPMessage.
type SMTPSendOptions struct {
	Host        string
	Port        int
	Username    string
	Password    string
	From        string
	To          []string
	Message     []byte
	UseSTARTTLS bool
}

// SendSMTPMessage performs a context-aware SMTP send. By default
// (UseSTARTTLS=true), the server must advertise STARTTLS; if it does not,
// the call fails closed before any auth or message data leaves the client,
// and the TLS handshake verifies the certificate against opts.Host.
//
// Setting UseSTARTTLS=false enables a cleartext escape hatch for local mail
// catchers (e.g. mailpit on 127.0.0.1) — the host must resolve only to
// loopback addresses; non-loopback cleartext is rejected because there is
// no legitimate use case for plaintext SMTP across the network.
//
// DNS rebinding is mitigated by resolving the host once and dialing the
// resolved IP; the TLS handshake still uses opts.Host as the SNI / verify
// name so a private-IP rebind cannot satisfy the cert.
func SendSMTPMessage(ctx context.Context, opts SMTPSendOptions) error {
	if opts.Host == "" {
		return errors.New("smtp: host required")
	}
	if opts.Port == 0 {
		opts.Port = 587
	}
	if len(opts.To) == 0 {
		return errors.New("smtp: at least one recipient required")
	}

	ips, err := net.DefaultResolver.LookupHost(ctx, opts.Host)
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("smtp: cannot resolve host: %w", err)
	}

	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			return fmt.Errorf("smtp: malformed resolved IP %q", ipStr)
		}
		if opts.UseSTARTTLS {
			if isPrivateOrReserved(ip) {
				return errors.New("smtp: host resolves to a private/loopback address")
			}
		} else if !ip.IsLoopback() {
			return fmt.Errorf("smtp: cleartext (TLS=off) is only allowed when host resolves to loopback (got %s)", ip)
		}
	}

	dialAddr := net.JoinHostPort(ips[0], strconv.Itoa(opts.Port))
	overallCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var dialer net.Dialer
	conn, err := dialer.DialContext(overallCtx, "tcp", dialAddr)
	if err != nil {
		return fmt.Errorf("smtp: dial: %w", err)
	}

	return runSMTPConversation(overallCtx, conn, opts)
}

// runSMTPConversation drives the SMTP protocol over an already-dialed
// connection. Split out from SendSMTPMessage so that tests can exercise the
// STARTTLS / cleartext branches against a loopback fake server without
// tripping the IP-validation gate.
//
// The caller is responsible for closing conn — runSMTPConversation hooks
// ctx cancellation so a hung peer cannot deadlock the caller.
func runSMTPConversation(ctx context.Context, conn net.Conn, opts SMTPSendOptions) error {
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()

	c, err := smtp.NewClient(conn, opts.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp: client: %w", err)
	}
	defer func() { _ = c.Close() }()

	if opts.UseSTARTTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("smtp: server does not advertise STARTTLS; refusing to send credentials in cleartext")
		}
		tlsCfg := &tls.Config{
			ServerName: opts.Host,
			MinVersion: tls.VersionTLS12,
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("smtp: starttls: %w", err)
		}
	}

	if opts.Username != "" {
		auth := smtp.PlainAuth("", opts.Username, opts.Password, opts.Host)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("smtp: auth: %w", err)
		}
	}

	if err := c.Mail(opts.From); err != nil {
		return fmt.Errorf("smtp: mail from: %w", err)
	}
	for _, addr := range opts.To {
		if err := c.Rcpt(addr); err != nil {
			return fmt.Errorf("smtp: rcpt to %s: %w", addr, err)
		}
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp: data: %w", err)
	}
	if _, err := w.Write(opts.Message); err != nil {
		return fmt.Errorf("smtp: write data: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp: close data: %w", err)
	}

	return c.Quit()
}
