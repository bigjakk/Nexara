package crypto

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"testing"
)

const (
	tagPurpose = "nexara test purpose v1"
	// otherKey is a second valid key, unrelated to validKey (encrypt_test.go).
	otherKey = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
)

// TestTagKnownAnswer pins the whole derivation to values computed independently,
// outside Go: HKDF-SHA256 (RFC 5869) of validKey with the empty salt and the
// purpose as info, 32 bytes out, then HMAC-SHA256 of the message under that
// subkey. A tag changes only if one of those choices does, and every token ever
// handed out under it stops matching when it does — so a change here is a
// decision, not a refactor.
func TestTagKnownAnswer(t *testing.T) {
	for _, tt := range []struct {
		name, purpose, want string
	}{
		{"the purpose of the vector", tagPurpose, "d22f3aae90a362b105c62cfcf58cbbbd948f922e4460a6a5bcea23fdaa27b484"},
		{"another purpose", "nexara test purpose v2", "9776a68e788f090a923311b40d8b513888959175e66e2d249bbd6451e2192601"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Tag(validKey, tt.purpose, []byte("message"))
			if err != nil {
				t.Fatalf("Tag() error = %v", err)
			}
			if hex.EncodeToString(got) != tt.want {
				t.Errorf("Tag() = %x, want %s", got, tt.want)
			}
		})
	}
}

// TestTagMatchesAnIndependentDerivation derives the subkey by hand from RFC 5869's
// definition, with nothing but HMAC, and compares: what the test pins is the
// construction, not the stdlib's implementation of it.
func TestTagMatchesAnIndependentDerivation(t *testing.T) {
	key, err := hex.DecodeString(validKey)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Extract with the default salt, HashLen zero bytes; expand one block, whose
	// counter byte is 0x01.
	extract := hmac.New(sha256.New, make([]byte, sha256.Size))
	extract.Write(key)
	prk := extract.Sum(nil)
	expand := hmac.New(sha256.New, prk)
	expand.Write([]byte(tagPurpose))
	expand.Write([]byte{0x01})
	sub := expand.Sum(nil)
	mac := hmac.New(sha256.New, sub)
	mac.Write([]byte("message"))
	want := mac.Sum(nil)

	got, err := Tag(validKey, tagPurpose, []byte("message"))
	if err != nil {
		t.Fatalf("Tag() error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("Tag() = %x, want %x", got, want)
	}

	// And the encryption key itself is not what the tag is made under: a tag that
	// was HMAC(key, msg) would let anyone who ever sees one such tag for a purpose
	// replay it for any other.
	direct := hmac.New(sha256.New, key)
	direct.Write([]byte("message"))
	if bytes.Equal(got, direct.Sum(nil)) {
		t.Error("Tag() is HMAC under the encryption key itself, not under a derived subkey")
	}
}

func TestTagIsDeterministicAndFullLength(t *testing.T) {
	a, err := Tag(validKey, tagPurpose, []byte("message"))
	if err != nil {
		t.Fatalf("Tag() error = %v", err)
	}
	b, err := Tag(validKey, tagPurpose, []byte("message"))
	if err != nil {
		t.Fatalf("Tag() error = %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("the same inputs gave %x and %x", a, b)
	}
	if len(a) != sha256.Size {
		t.Errorf("the tag is %d bytes, want %d", len(a), sha256.Size)
	}
}

// TestTagDiffersByEveryInput: the key, the purpose and the message each change
// the tag, which is what makes it bind to all three.
func TestTagDiffersByEveryInput(t *testing.T) {
	base, err := Tag(validKey, tagPurpose, []byte("message"))
	if err != nil {
		t.Fatalf("Tag() error = %v", err)
	}
	for _, tt := range []struct {
		name, key, purpose, msg string
	}{
		{"another message", validKey, tagPurpose, "message."},
		{"an empty message", validKey, tagPurpose, ""},
		{"another purpose", validKey, tagPurpose + "x", "message"},
		{"another purpose, one byte shorter", validKey, tagPurpose[:len(tagPurpose)-1], "message"},
		{"another key", otherKey, tagPurpose, "message"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Tag(tt.key, tt.purpose, []byte(tt.msg))
			if err != nil {
				t.Fatalf("Tag() error = %v", err)
			}
			if bytes.Equal(got, base) {
				t.Errorf("%s gave the same tag as the base case: %x", tt.name, got)
			}
		})
	}
}

// TestTagRefusesWhatEncryptRefuses: the same key rules, and the same error.
func TestTagRefusesWhatEncryptRefuses(t *testing.T) {
	for _, key := range []string{
		"",
		"0123456789abcdef",
		validKey + "ff",
		"zz23456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	} {
		if _, err := Tag(key, tagPurpose, []byte("message")); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Tag(%q) error = %v, want ErrInvalidKey", key, err)
		}
	}
}

func TestTagRefusesAnEmptyPurpose(t *testing.T) {
	if _, err := Tag(validKey, "", []byte("message")); !errors.Is(err, ErrEmptyPurpose) {
		t.Errorf("Tag() with no purpose: error = %v, want ErrEmptyPurpose", err)
	}
}

// TestTagIsSafeForConcurrentUse runs the cached path from many goroutines at once
// — the cache is filled by whichever gets there first — and holds them all to the
// one answer. The race detector does the rest.
func TestTagIsSafeForConcurrentUse(t *testing.T) {
	want, err := Tag(validKey, "nexara test purpose v9", []byte("message"))
	if err != nil {
		t.Fatalf("Tag() error = %v", err)
	}
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				got, err := Tag(validKey, "nexara test purpose v9", []byte("message"))
				if err != nil || !bytes.Equal(got, want) {
					t.Errorf("Tag() = %x, %v; want %x", got, err, want)
					return
				}
			}
		}()
	}
	wg.Wait()
}
