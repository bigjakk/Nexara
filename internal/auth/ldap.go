package auth

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/google/uuid"
)

// Sentinel errors for classified LDAP failures.
var (
	ErrLDAPConnection      = errors.New("ldap: connection failed")
	ErrLDAPBindCredentials = errors.New("ldap: invalid bind credentials")
	ErrLDAPUserNotFound    = errors.New("ldap: user not found")
	ErrLDAPSearchFailed    = errors.New("ldap: search failed")
	ErrLDAPUserBindFailed  = errors.New("ldap: user password incorrect")
	// ErrLDAPFilterInvalid is a user or group filter that go-ldap cannot compile,
	// which no directory is ever asked: go-ldap v3.4.15 compiles more strictly than
	// v3.4.14 did (a set or a nested filter must end in ")"), so a filter stored
	// before the upgrade may be one.
	ErrLDAPFilterInvalid = errors.New("ldap: the filter is not a valid LDAP filter")
)

// ValidateFilters reports the first of the two filter templates that does not
// compile, with its placeholder filled in, as ErrLDAPFilterInvalid; an empty one is
// not checked. The value filled in is escaped (ldap.EscapeFilter) as a sign-in's is,
// and an escaped value adds no structure, so the template alone decides. The error
// names the field and go-ldap's reason, and nothing typed into a sign-in form.
func ValidateFilters(userFilter, groupFilter string) error {
	for _, f := range []struct{ field, filter, placeholder, sample string }{
		{"user_filter", userFilter, "{{username}}", "alice"},
		{"group_filter", groupFilter, "{{userDN}}", "uid=alice,dc=example,dc=com"},
	} {
		if f.filter == "" {
			continue
		}
		_, err := ldap.CompileFilter(strings.ReplaceAll(f.filter, f.placeholder, ldap.EscapeFilter(f.sample)))
		if err != nil {
			var le *ldap.Error
			if errors.As(err, &le) && le.Err != nil {
				err = le.Err
			}
			return fmt.Errorf("%w: %s: %v", ErrLDAPFilterInvalid, f.field, err)
		}
	}
	return nil
}

// LDAPConfig holds the configuration for connecting to an LDAP/AD server.
type LDAPConfig struct {
	ServerURL            string
	StartTLS             bool
	SkipTLSVerify        bool
	BindDN               string
	BindPassword         string
	SearchBaseDN         string
	UserFilter           string // e.g. (|(uid={{username}})(mail={{username}}))
	UsernameAttribute    string // e.g. uid
	EmailAttribute       string // e.g. mail
	DisplayNameAttribute string // e.g. cn
	GroupSearchBaseDN    string
	GroupFilter          string // e.g. (member={{userDN}})
	GroupAttribute       string // e.g. cn
	// RequestTimeout bounds each request on the connection; 0 is ldapRequestTimeout.
	// Only tests set it.
	RequestTimeout time.Duration
}

// ldapRequestTimeout bounds every request, for sign-in and sync alike: go-ldap waits
// for ever by default, and a directory that accepts the connection and never answers
// would hold a sign-in, or the sync and its scheduler slot, until a restart.
const ldapRequestTimeout = time.Minute

// LDAPUser represents a user found in the LDAP directory.
type LDAPUser struct {
	DN          string
	Username    string
	Email       string
	DisplayName string
	Groups      []string
	// EntryID is the entry's stable identifier in canonical text (entryIDOf), "" when
	// the directory exposes neither entryUUID nor objectGUID.
	EntryID string
	// GroupLookupFailed says the group search did not answer, so Groups says nothing:
	// a caller must leave the user's roles as they are rather than read "no groups".
	GroupLookupFailed bool
}

// LDAPClient wraps LDAP operations.
type LDAPClient struct {
	cfg LDAPConfig
}

// NewLDAPClient creates a new LDAP client.
func NewLDAPClient(cfg LDAPConfig) *LDAPClient {
	return &LDAPClient{cfg: cfg}
}

// tlsConfig is the TLS configuration of both ldaps:// and StartTLS. ServerName is the
// configured host: StartTLS hands the config to tls.Client, which, unlike the dial of
// ldaps://, does not infer it from the address, and with no name and verification on
// every handshake failed.
func (c *LDAPClient) tlsConfig() *tls.Config {
	cfg := &tls.Config{
		InsecureSkipVerify: c.cfg.SkipTLSVerify, //nolint:gosec // user-configurable for self-signed certs
	}
	if u, err := url.Parse(c.cfg.ServerURL); err == nil {
		cfg.ServerName = u.Hostname()
	}
	return cfg
}

func (c *LDAPClient) dial() (*ldap.Conn, error) {
	tlsCfg := c.tlsConfig()

	isLDAPS := strings.HasPrefix(c.cfg.ServerURL, "ldaps://")
	if !isLDAPS && !c.cfg.StartTLS {
		slog.Warn("LDAP connection is plaintext — credentials will be sent unencrypted",
			"server_url", c.cfg.ServerURL)
	}

	var conn *ldap.Conn
	var err error

	if isLDAPS {
		conn, err = ldap.DialURL(c.cfg.ServerURL, ldap.DialWithTLSConfig(tlsCfg))
	} else {
		conn, err = ldap.DialURL(c.cfg.ServerURL)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrLDAPConnection, err)
	}

	timeout := c.cfg.RequestTimeout
	if timeout <= 0 {
		timeout = ldapRequestTimeout
	}
	conn.SetTimeout(timeout)

	if c.cfg.StartTLS && !isLDAPS {
		if err := conn.StartTLS(tlsCfg); err != nil {
			conn.Close()
			return nil, fmt.Errorf("%w: StartTLS handshake failed: %v", ErrLDAPConnection, err)
		}
	}

	return conn, nil
}

func (c *LDAPClient) serviceBind(conn ldap.Client) error {
	if c.cfg.BindDN == "" {
		return nil
	}
	if err := conn.Bind(c.cfg.BindDN, c.cfg.BindPassword); err != nil {
		return fmt.Errorf("%w: %v", ErrLDAPBindCredentials, err)
	}
	return nil
}

// TestConnection verifies the server is reachable and the service account can bind.
func (c *LDAPClient) TestConnection() error {
	conn, err := c.dial()
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := c.serviceBind(conn); err != nil {
		return err
	}

	return nil
}

// LDAPSession is one connection to the directory, bound as the service account. A
// sign-in is two steps on it, so that the caller can act between them: Search finds
// the entry the typed name names, and Authenticate binds as that entry with the
// password. The caller closes it.
type LDAPSession struct {
	client *LDAPClient
	conn   *ldap.Conn
}

// Open dials the directory and binds as the service account.
func (c *LDAPClient) Open() (*LDAPSession, error) {
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	if err := c.serviceBind(conn); err != nil {
		conn.Close()
		return nil, err
	}
	return &LDAPSession{client: c, conn: conn}, nil
}

// Close closes the connection.
func (s *LDAPSession) Close() { s.conn.Close() }

// Search finds the entry username names (the user filter decides which attributes it
// may be: uid, mail, sAMAccountName, the UPN), without its groups.
func (s *LDAPSession) Search(username string) (*LDAPUser, error) {
	return s.client.searchUser(s.conn, username)
}

// Authenticate binds as user's entry with password, and on success fills in its
// groups. An empty password is refused before the bind: to a directory it is an
// unauthenticated bind, which succeeds.
func (s *LDAPSession) Authenticate(user *LDAPUser, password string) error {
	if password == "" {
		return fmt.Errorf("%w: empty password not allowed", ErrLDAPUserBindFailed)
	}
	if err := s.conn.Bind(user.DN, password); err != nil {
		return fmt.Errorf("%w: %v", ErrLDAPUserBindFailed, err)
	}
	// Fetch groups (re-bind as service account first)
	s.client.fillGroups(s.conn, user, true)
	return nil
}

// SearchUser looks up a user without authenticating them.
func (c *LDAPClient) SearchUser(username string) (*LDAPUser, error) {
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := c.serviceBind(conn); err != nil {
		return nil, err
	}

	user, err := c.searchUser(conn, username)
	if err != nil {
		return nil, err
	}

	c.fillGroups(conn, user, false)
	return user, nil
}

// fillGroups reads user's groups, after re-binding as the service account when rebind
// says so; a bind or search that fails marks the lookup failed instead.
func (c *LDAPClient) fillGroups(conn ldap.Client, user *LDAPUser, rebind bool) {
	if rebind {
		if err := c.serviceBind(conn); err != nil {
			user.GroupLookupFailed = true
			return
		}
	}
	groups, err := c.getUserGroups(conn, user.DN)
	if errors.Is(err, ErrLDAPFilterInvalid) {
		slog.Warn("ldap: the stored group filter is not a valid LDAP filter, so no group search can run; "+
			"the account's roles are left as they are", "error", err)
	}
	user.Groups, user.GroupLookupFailed = groups, err != nil
}

func (c *LDAPClient) searchUser(conn *ldap.Conn, username string) (*LDAPUser, error) {
	if err := ValidateFilters(c.cfg.UserFilter, ""); err != nil {
		return nil, err
	}
	filter := strings.ReplaceAll(c.cfg.UserFilter, "{{username}}", ldap.EscapeFilter(username))

	return c.searchOne(conn, filter)
}

// SearchUserByEntryID finds the entry whose identifier is id (entryIDOf's form), under
// the search base and without the user filter, and without its groups: the caller
// checks the entry against the filter by a name it answers to (SearchUser). Not found
// is ErrLDAPUserNotFound.
func (c *LDAPClient) SearchUserByEntryID(id string) (*LDAPUser, error) {
	filter, err := entryIDFilter(id)
	if err != nil {
		return nil, err
	}
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if err := c.serviceBind(conn); err != nil {
		return nil, err
	}
	return c.searchOne(conn, filter)
}

// entryAttributes are the identifiers asked for with every entry: entryUUID (RFC 4530;
// OpenLDAP, 389) is operational and returned only when named, objectGUID (Active
// Directory) is binary. A directory ignores one it does not know.
var entryAttributes = []string{"entryUUID", "objectGUID"}

// searchOne runs a compiled filter under the search base and reads the one entry.
func (c *LDAPClient) searchOne(conn ldap.Client, filter string) (*LDAPUser, error) {
	attrs := append([]string{"dn", c.cfg.UsernameAttribute, c.cfg.EmailAttribute, c.cfg.DisplayNameAttribute}, entryAttributes...)

	searchReq := ldap.NewSearchRequest(
		c.cfg.SearchBaseDN,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		1,  // sizeLimit
		30, // timeLimit seconds
		false,
		filter,
		attrs,
		nil,
	)

	result, err := conn.Search(searchReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrLDAPSearchFailed, err)
	}

	if len(result.Entries) == 0 {
		// Not the filter: it holds what was typed into the sign-in form, which is
		// sometimes a password, and this error reaches logs.
		return nil, fmt.Errorf("%w: no entries matched in base %q", ErrLDAPUserNotFound, c.cfg.SearchBaseDN)
	}

	entry := result.Entries[0]
	return &LDAPUser{
		DN:          entry.DN,
		Username:    entry.GetAttributeValue(c.cfg.UsernameAttribute),
		Email:       entry.GetAttributeValue(c.cfg.EmailAttribute),
		DisplayName: entry.GetAttributeValue(c.cfg.DisplayNameAttribute),
		EntryID:     entryIDOf(entry),
	}, nil
}

// entryIDOf is an entry's stable identifier in ONE canonical text form, so that it is
// only ever compared as text: "objectGUID:" and the GUID as Active Directory prints it
// (the first three fields little-endian), read from the attribute's 16 raw bytes, or
// "entryUUID:" and the UUID in lower case. "" when the entry carries neither in a form
// that parses: the directory then works as before, by name and address.
func entryIDOf(entry *ldap.Entry) string {
	if raw := entry.GetRawAttributeValue("objectGUID"); len(raw) == 16 {
		var b [16]byte
		copy(b[:], raw)
		b[0], b[1], b[2], b[3] = b[3], b[2], b[1], b[0]
		b[4], b[5] = b[5], b[4]
		b[6], b[7] = b[7], b[6]
		return "objectGUID:" + uuid.UUID(b).String()
	}
	if u, err := uuid.Parse(strings.TrimSpace(entry.GetAttributeValue("entryUUID"))); err == nil {
		return "entryUUID:" + u.String()
	}
	return ""
}

// entryIDFilter is the search filter that finds the entry an identifier names: an
// objectGUID as its 16 bytes, each escaped, as Active Directory matches it.
func entryIDFilter(id string) (string, error) {
	kind, value, _ := strings.Cut(id, ":")
	u, err := uuid.Parse(value)
	if err != nil {
		return "", fmt.Errorf("%w: not an entry identifier", ErrLDAPSearchFailed)
	}
	switch kind {
	case "entryUUID":
		return "(entryUUID=" + u.String() + ")", nil
	case "objectGUID":
		b := u
		b[0], b[1], b[2], b[3] = b[3], b[2], b[1], b[0]
		b[4], b[5] = b[5], b[4]
		b[6], b[7] = b[7], b[6]
		var f strings.Builder
		f.WriteString("(objectGUID=")
		for _, x := range b {
			fmt.Fprintf(&f, "\\%02x", x)
		}
		f.WriteString(")")
		return f.String(), nil
	}
	return "", fmt.Errorf("%w: not an entry identifier", ErrLDAPSearchFailed)
}

// getUserGroups lists the DNs of the user's groups: none when no group search is
// configured, an error when the search fails (a timeout, a size limit, no read access).
func (c *LDAPClient) getUserGroups(conn ldap.Client, userDN string) ([]string, error) {
	if c.cfg.GroupSearchBaseDN == "" || c.cfg.GroupFilter == "" {
		return nil, nil
	}
	if err := ValidateFilters("", c.cfg.GroupFilter); err != nil {
		return nil, err
	}

	filter := strings.ReplaceAll(c.cfg.GroupFilter, "{{userDN}}", ldap.EscapeFilter(userDN))

	searchReq := ldap.NewSearchRequest(
		c.cfg.GroupSearchBaseDN,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		0, // no size limit
		30,
		false,
		filter,
		[]string{"dn", c.cfg.GroupAttribute},
		nil,
	)

	result, err := conn.Search(searchReq)
	if err != nil {
		return nil, fmt.Errorf("%w: group search: %v", ErrLDAPSearchFailed, err)
	}

	groups := make([]string, 0, len(result.Entries))
	for _, entry := range result.Entries {
		groups = append(groups, entry.DN)
	}
	return groups, nil
}
