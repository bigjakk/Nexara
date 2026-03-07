package auth

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"strings"

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
}

// LDAPUser represents a user found in the LDAP directory.
type LDAPUser struct {
	DN          string
	Username    string
	Email       string
	DisplayName string
	Groups      []string
}

// LDAPClient wraps LDAP operations.
type LDAPClient struct {
	cfg LDAPConfig
}

// NewLDAPClient creates a new LDAP client.
func NewLDAPClient(cfg LDAPConfig) *LDAPClient {
	return &LDAPClient{cfg: cfg}
}

func (c *LDAPClient) dial() (*ldap.Conn, error) {
	tlsCfg := &tls.Config{
		InsecureSkipVerify: c.cfg.SkipTLSVerify, //nolint:gosec // user-configurable for self-signed certs
	}

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

	if c.cfg.StartTLS && !isLDAPS {
		if err := conn.StartTLS(tlsCfg); err != nil {
			conn.Close()
			return nil, fmt.Errorf("%w: StartTLS handshake failed: %v", ErrLDAPConnection, err)
		}
	}

	return conn, nil
}

func (c *LDAPClient) serviceBind(conn *ldap.Conn) error {
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

// Authenticate verifies user credentials against LDAP.
// Returns the user info on success, or an error if auth fails.
func (c *LDAPClient) Authenticate(username, password string) (*LDAPUser, error) {
	if password == "" {
		return nil, fmt.Errorf("empty password not allowed")
	}

	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	// Bind with service account to search
	if err := c.serviceBind(conn); err != nil {
		return nil, err
	}

	// Search for the user
	user, err := c.searchUser(conn, username)
	if err != nil {
		return nil, err
	}

	// Re-bind as the found user to verify their password
	if err := conn.Bind(user.DN, password); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrLDAPUserBindFailed, err)
	}

	// Fetch groups (re-bind as service account first)
	if err := c.serviceBind(conn); err == nil {
		user.Groups = c.getUserGroups(conn, user.DN)
	}

	return user, nil
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

	user.Groups = c.getUserGroups(conn, user.DN)
	return user, nil
}

func (c *LDAPClient) searchUser(conn *ldap.Conn, username string) (*LDAPUser, error) {
	filter := strings.ReplaceAll(c.cfg.UserFilter, "{{username}}", ldap.EscapeFilter(username))

	attrs := []string{"dn", c.cfg.UsernameAttribute, c.cfg.EmailAttribute, c.cfg.DisplayNameAttribute}

	searchReq := ldap.NewSearchRequest(
		c.cfg.SearchBaseDN,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		1, // sizeLimit
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
		return nil, fmt.Errorf("%w: no entries matched filter %q in base %q", ErrLDAPUserNotFound, filter, c.cfg.SearchBaseDN)
	}

	entry := result.Entries[0]
	return &LDAPUser{
		DN:          entry.DN,
		Username:    entry.GetAttributeValue(c.cfg.UsernameAttribute),
		Email:       entry.GetAttributeValue(c.cfg.EmailAttribute),
		DisplayName: entry.GetAttributeValue(c.cfg.DisplayNameAttribute),
	}, nil
}

func (c *LDAPClient) getUserGroups(conn *ldap.Conn, userDN string) []string {
	if c.cfg.GroupSearchBaseDN == "" || c.cfg.GroupFilter == "" {
		return nil
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
		return nil
	}

	groups := make([]string, 0, len(result.Entries))
	for _, entry := range result.Entries {
		groups = append(groups, entry.DN)
	}
	return groups
}
