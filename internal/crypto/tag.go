package crypto

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
)

// ErrEmptyPurpose indicates a tag was asked for without saying what it is for.
// The purpose is what keeps one use of the key from being replayed as another,
// so there is no such thing as a tag without one.
var ErrEmptyPurpose = errors.New("tag purpose must not be empty")

// tagKeyID names one derived key: the same encryption key under two purposes
// is two keys.
type tagKeyID struct{ hexKey, purpose string }

// tagKeyCache memoises the derived subkey per (encryption key, purpose), as
// aeadCache memoises the AEAD per key. The set is as small as the number of
// purposes in the program, so it does not grow.
var tagKeyCache sync.Map // map[tagKeyID][]byte

// tagKeyFor derives the subkey that purpose's tags are made under: HKDF-SHA256
// (RFC 5869) over the 32-byte encryption key, with no salt — the input is
// already a uniformly random key, which is the case HKDF's salt is optional
// for — and purpose as the info string, 32 bytes out.
//
// The raw encryption key is also the AES-GCM key (aeadFor, encrypt.go). It goes
// into this derivation and nowhere else in this file, and a new use of it must come
// through this derivation with a purpose of its own, not take the raw key a third
// time (see the note above aeadCache in encrypt.go).
//
// The encryption key is checked by parseKey, so a key that Encrypt would refuse
// is refused here, and for the same reason: a tag made under a malformed key
// would be a tag under a key nobody chose.
func tagKeyFor(hexKey, purpose string) ([]byte, error) {
	id := tagKeyID{hexKey, purpose}
	if v, ok := tagKeyCache.Load(id); ok {
		return v.([]byte), nil
	}

	key, err := parseKey(hexKey)
	if err != nil {
		return nil, err
	}
	sub, err := hkdf.Key(sha256.New, key, nil, purpose, sha256.Size)
	if err != nil {
		return nil, fmt.Errorf("derive tag key: %w", err)
	}
	actual, _ := tagKeyCache.LoadOrStore(id, sub)
	return actual.([]byte), nil
}

// Tag returns the HMAC-SHA256 of msg under a subkey derived from the encryption
// key and bound to purpose, which names what the tag is for ("nexara
// node-config cas token v1"). The same message under another purpose, or another
// key, gives an unrelated tag: a tag minted for one use cannot be presented as
// another's, and the encryption key itself is never used to authenticate
// anything, only to derive.
//
// Callers compare a tag they are given with the one they compute using
// hmac.Equal, never with == or bytes.Equal, which return at the first byte that
// differs and so tell a caller how much of a guess was right.
//
// msg must be an unambiguous encoding of what is being authenticated: two
// different tuples of fields must never encode to the same bytes, or the tag
// cannot tell them apart. Length-prefix each variable-length field.
func Tag(hexKey, purpose string, msg []byte) ([]byte, error) {
	if purpose == "" {
		return nil, ErrEmptyPurpose
	}
	sub, err := tagKeyFor(hexKey, purpose)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, sub)
	mac.Write(msg)
	return mac.Sum(nil), nil
}
