package auth

// TokenKind says what a validly signed token may be used for.
//
// Claims is ONE struct for three kinds of token (see its comment), told apart by
// which optional scope marker is set. That is a fine shape for issuing them and a
// poor one for accepting them if each caller works the kind out from the fields it
// happens to remember: the API's authentication middleware used to admit "a JWT
// that is not console-scoped and not WS-scoped", which is an exclusion — a kind of
// token added tomorrow, with a marker of its own, would be an API session until
// somebody remembered to add a third test to two functions. The kind is decided
// here, once, and the caller asks IsSession.
//
// TestClaimsKind_EveryFieldIsClassified is what makes this hold: it fails when a
// field is added to Claims that it has not been told the role of, so that adding a
// marker and teaching Kind about it are one change, and not two that a review has to
// connect.
type TokenKind int

const (
	// TokenKindUnknown is claims that fit no kind this code knows: both scope markers
	// set at once, or a WSScope that is not a value this code issues. The zero value,
	// on purpose: a Kind nobody filled in is a token nothing accepts.
	TokenKindUnknown TokenKind = iota
	// TokenKindSession is an interactive session's access token: the one kind sent in
	// Authorization: Bearer on API calls, and the only one that authenticates a caller.
	TokenKindSession
	// TokenKindConsole is a console-scoped token: one WebSocket upgrade, for the exact
	// cluster, node, guest and type in its scope.
	TokenKindConsole
	// TokenKindWSHub is a WebSocket hub token: the generic /ws upgrade and nothing else.
	TokenKindWSHub
)

// Kind classifies the claims. It is positive about every kind it returns: a session
// is claims with NO scope marker, a console token has a console scope and nothing
// else, a hub token has exactly the hub scope and nothing else, and whatever matches
// none of those — two markers at once, a marker it does not know — is
// TokenKindUnknown.
func (c *Claims) Kind() TokenKind {
	hasConsole := c.ConsoleScope != nil
	switch {
	case !hasConsole && c.WSScope == "":
		return TokenKindSession
	case hasConsole && c.WSScope == "":
		return TokenKindConsole
	case !hasConsole && c.WSScope == WSScopeHub:
		return TokenKindWSHub
	}
	return TokenKindUnknown
}

// IsSession reports whether the token is an interactive session's access token. It
// is the ONE question the API's authentication asks of a token's kind — authRequired
// and authOptional both — so a request is a session (and, on the routes that care,
// admitted as one) only through here.
func (c *Claims) IsSession() bool { return c.Kind() == TokenKindSession }
