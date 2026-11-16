package auth

import (
	"errors"
	"net"
	"testing"
	"time"
)

// TestLDAPTLSConfig_NamesTheConfiguredHost: StartTLS verifies the directory's certificate
// against the host of the server URL, so the TLS config must carry it.
func TestLDAPTLSConfig_NamesTheConfiguredHost(t *testing.T) {
	t.Parallel()
	for url, want := range map[string]string{
		"ldap://dc01.example.com:389": "dc01.example.com",
		"ldaps://dc01.example.com":    "dc01.example.com",
		"ldap://[2001:db8::10]:389":   "2001:db8::10",
	} {
		if got := NewLDAPClient(LDAPConfig{ServerURL: url, StartTLS: true}).tlsConfig().ServerName; got != want {
			t.Errorf("%s: ServerName %q, want %q", url, got, want)
		}
	}
}

// TestLDAPOpen_ADirectoryThatNeverAnswersFails: a server that accepts the connection and
// never answers ends the service bind at the request timeout with the bind's error,
// instead of holding the caller for ever.
func TestLDAPOpen_ADirectoryThatNeverAnswersFails(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	held := make(chan net.Conn, 4)
	t.Cleanup(func() {
		close(held)
		for conn := range held {
			_ = conn.Close()
		}
	})
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			held <- conn // held open, never answered
		}
	}()
	client := NewLDAPClient(LDAPConfig{ServerURL: "ldap://" + ln.Addr().String(), BindDN: "cn=nexara,dc=example,dc=com",
		BindPassword: "x", RequestTimeout: 100 * time.Millisecond})
	done := make(chan error, 1)
	go func() {
		s, err := client.Open()
		if s != nil {
			s.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrLDAPBindCredentials) {
			t.Errorf("Open = %v, want the bind's error", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Open is still waiting on a directory that never answers")
	}
}
