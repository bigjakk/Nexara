package auth

import (
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"
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

// groupDirectory answers the group search with err, or with entries; the embedded nil
// Client fails loudly if anything else is asked.
type groupDirectory struct {
	ldap.Client
	entries []*ldap.Entry
	err     error
}

func (d groupDirectory) Bind(string, string) error { return nil }

func (d groupDirectory) Search(*ldap.SearchRequest) (*ldap.SearchResult, error) {
	if d.err != nil {
		return nil, d.err
	}
	return &ldap.SearchResult{Entries: d.entries}, nil
}

// TestFillGroups_AFailedGroupSearchIsNotNoGroups: a group search that fails marks the
// lookup failed (the caller then leaves the user's roles alone); one that succeeded
// with no entries is "no groups", and its entries are the groups.
func TestFillGroups_AFailedGroupSearchIsNotNoGroups(t *testing.T) {
	t.Parallel()
	c := NewLDAPClient(LDAPConfig{GroupSearchBaseDN: "ou=groups,dc=example,dc=com", GroupFilter: "(member={{userDN}})"})
	for _, tc := range []struct {
		dir    groupDirectory
		failed bool
		groups int
	}{
		{groupDirectory{err: errors.New("time limit exceeded")}, true, 0},
		{groupDirectory{}, false, 0},
		{groupDirectory{entries: []*ldap.Entry{{DN: "cn=operators,ou=groups,dc=example,dc=com"}}}, false, 1},
	} {
		u := &LDAPUser{DN: "uid=carol,dc=example,dc=com"}
		c.fillGroups(tc.dir, u, true)
		if u.GroupLookupFailed != tc.failed || len(u.Groups) != tc.groups {
			t.Errorf("search err %v, %d entries: failed %t, %d groups; want %t, %d", tc.dir.err, len(tc.dir.entries), u.GroupLookupFailed, len(u.Groups), tc.failed, tc.groups)
		}
	}
}

// TestValidateFilters_AFilterGoLDAPNoLongerCompilesIsNamed: go-ldap v3.4.15 refuses a set or a
// nested filter that does not end in ")", which v3.4.14 skipped, so a filter stored before the
// upgrade can stop compiling. It is reported as ErrLDAPFilterInvalid, naming the field, at save,
// at sign-in (searchUser) and at sync, rather than as a search that found nobody.
func TestValidateFilters_AFilterGoLDAPNoLongerCompilesIsNamed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		user, group string
		field       string // "" when both compile
	}{
		{"(|(uid={{username}})(mail={{username}}))", "(member={{userDN}})", ""},
		{"(|(uid={{username}})(mail={{username}})x", "", "user_filter"}, // a stray byte where ")" belongs
		{"((uid={{username}})]", "", "user_filter"},
		{"(uid={{username}})", "(&(member={{userDN}})(objectClass=group)!", "group_filter"},
	} {
		err := ValidateFilters(tc.user, tc.group)
		if tc.field == "" {
			if err != nil {
				t.Errorf("%q, %q: %v, want both to compile", tc.user, tc.group, err)
			}
			continue
		}
		if !errors.Is(err, ErrLDAPFilterInvalid) || !strings.Contains(err.Error(), tc.field) {
			t.Errorf("%q, %q: %v, want ErrLDAPFilterInvalid naming %s", tc.user, tc.group, err, tc.field)
		}
	}
	// The sign-in's search says the same before it asks any directory (a nil conn).
	c := NewLDAPClient(LDAPConfig{UserFilter: "(|(uid={{username}})(mail={{username}})x"})
	if _, err := c.searchUser(nil, "alice"); !errors.Is(err, ErrLDAPFilterInvalid) {
		t.Errorf("searchUser = %v, want ErrLDAPFilterInvalid", err)
	}
}

// TestEntryIDOf_OneCanonicalTextForm: an Active Directory objectGUID is read as its 16
// raw bytes and printed as AD prints a GUID (first three fields little-endian), an
// entryUUID in lower case, and anything else is no identifier; the sync's search by
// identifier turns the text back into the same bytes, each escaped.
func TestEntryIDOf_OneCanonicalTextForm(t *testing.T) {
	t.Parallel()
	guid := []byte{0x03, 0x02, 0x01, 0x00, 0x05, 0x04, 0x07, 0x06, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f}
	attr := func(name string, raw []byte) *ldap.EntryAttribute {
		return &ldap.EntryAttribute{Name: name, Values: []string{string(raw)}, ByteValues: [][]byte{raw}}
	}
	for _, tc := range []struct {
		name  string
		attrs []*ldap.EntryAttribute
		want  string
	}{
		{"objectGUID", []*ldap.EntryAttribute{attr("objectGUID", guid)}, "objectGUID:00010203-0405-0607-0809-0a0b0c0d0e0f"},
		{"entryUUID", []*ldap.EntryAttribute{attr("entryUUID", []byte("6C0A1F6E-0000-4000-8000-0000000000A1"))}, "entryUUID:6c0a1f6e-0000-4000-8000-0000000000a1"},
		{"neither", nil, ""},
		{"an objectGUID of the wrong length", []*ldap.EntryAttribute{attr("objectGUID", guid[:15])}, ""},
	} {
		if got := entryIDOf(&ldap.Entry{Attributes: tc.attrs}); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
	filter, err := entryIDFilter("objectGUID:00010203-0405-0607-0809-0a0b0c0d0e0f")
	if want := `(objectGUID=\03\02\01\00\05\04\07\06\08\09\0a\0b\0c\0d\0e\0f)`; err != nil || filter != want {
		t.Errorf("entryIDFilter = %q, %v; want %q", filter, err, want)
	}
}
