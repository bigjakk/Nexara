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
)

// Sentinel errors for classified LDAP failures.
var (
	ErrLDAPConnection      = errors.New("ldap: connection failed")
	ErrLDAPBindCredentials = errors.New("ldap: invalid bind credentials")
	ErrLDAPUserNotFound    = errors.New("ldap: user not found")
	ErrLDAPSearchFailed    = errors.New("ldap: search failed")
	ErrLDAPUserBindFailed  = errors.New("ldap: user password incorrect")
)

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
	user.Groups, user.GroupLookupFailed = groups, err != nil
}

func (c *LDAPClient) searchUser(conn *ldap.Conn, username string) (*LDAPUser, error) {
	filter := strings.ReplaceAll(c.cfg.UserFilter, "{{username}}", ldap.EscapeFilter(username))

	attrs := []string{"dn", c.cfg.UsernameAttribute, c.cfg.EmailAttribute, c.cfg.DisplayNameAttribute}

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
	}, nil
}

// getUserGroups lists the DNs of the user's groups: none when no group search is
// configured, an error when the search fails (a timeout, a size limit, no read access).
func (c *LDAPClient) getUserGroups(conn ldap.Client, userDN string) ([]string, error) {
	if c.cfg.GroupSearchBaseDN == "" || c.cfg.GroupFilter == "" {
		return nil, nil
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
