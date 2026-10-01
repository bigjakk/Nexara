package handlers

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"math"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/crypto"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// The save token: what a read of a node's config hands out in its `digest` field,
// and what the PUT that follows sends back, in place of Proxmox's own digest.
//
// Proxmox's digest is the unsalted SHA1 of the WHOLE /etc/pve/nodes/<node>/config,
// notes included, and Proxmox writes that file deterministically (write_node_config
// in pve-manager's PVE/NodeConfig.pm: the notes as '#' lines, then every other key
// sorted). A caller who is shown it, and can read the rest of the file through the
// settings and the ACME reads, can hash a guess at the notes and compare, offline,
// with no rate limit and no audit row. A caller who is not shown it but may write
// can still PUT candidate digests and tell a miss from a hit by the 409. So it is
// never handed out. What is handed out is an HMAC of it, under a key only Nexara
// has: it can be sent back, and nothing about the file can be recovered from it or
// tested against it.
//
// The token is bound to the cluster and the node it was minted for, so one read's
// token is not one that another node's save accepts. It is checked on the save by
// re-reading the node's CURRENT digest from Proxmox, minting the token for that,
// and comparing — so Nexara keeps no state, and a token minted before the file
// changed no longer matches. What goes to Proxmox is then the fresh digest, in its
// own form, so Proxmox's assert_if_modified still guards the window between that
// re-read and its write.

// nodeConfigTokenPurpose is the purpose the token's subkey is derived for. It
// names the use and the version: a tag made under it can be presented for nothing
// else, and a change to the encoding below is a new purpose, not an edit.
const nodeConfigTokenPurpose = "nexara node-config cas token v1"

// nodeConfigTokenPrefix leads every token. It is the version, in the clear, so
// that a later format can be told from this one without trying it.
const nodeConfigTokenPrefix = "v1."

// nodeConfigChangedMessage is what a save is told when the node's config is not
// what the save was based on: the token does not match the file as it is now. It
// is also what Proxmox's own digest check is mapped to (mapNodeConfigError), so
// the two ways of finding out say the same thing, in the same words, and a caller
// cannot tell from the answer which of them caught it.
const nodeConfigChangedMessage = "The node's configuration changed since it was read — reload and try again."

// errNodeConfigTokenField is a field that the token's encoding cannot count. A node
// name and a digest are a few dozen bytes; this is here so that the length prefix
// is never a truncated one.
var errNodeConfigTokenField = errors.New("a field of the node config token is too long to be encoded")

// appendFieldLength appends the big-endian uint32 that leads a variable-length
// field of n bytes. A field a uint32 cannot count is refused and not truncated: a
// truncated length would let two different fields give one encoding, which is what
// the length is there to prevent.
func appendFieldLength(msg []byte, n int) ([]byte, error) {
	if n < 0 || int64(n) > math.MaxUint32 {
		return nil, errNodeConfigTokenField
	}
	return binary.BigEndian.AppendUint32(msg, uint32(n)), nil //nolint:gosec // G115: bounds checked above
}

// nodeConfigTokenMessage is what the tag is computed over: the cluster, the node
// and Proxmox's digest, encoded so that two different triples never give the same
// bytes. The cluster is a fixed 16 bytes; the node and the digest are variable, so
// each is led by its length (a big-endian uint32). Without the lengths, node "ab"
// with digest "c…" and node "a" with digest "bc…" would be one message.
func nodeConfigTokenMessage(clusterID uuid.UUID, node, digest string) ([]byte, error) {
	msg := make([]byte, 0, len(clusterID)+8+len(node)+len(digest))
	msg = append(msg, clusterID[:]...)
	var err error
	if msg, err = appendFieldLength(msg, len(node)); err != nil {
		return nil, err
	}
	msg = append(msg, node...)
	if msg, err = appendFieldLength(msg, len(digest)); err != nil {
		return nil, err
	}
	msg = append(msg, digest...)
	return msg, nil
}

// nodeConfigToken is the token for a node's config as it is now: the prefix and
// the unpadded base64url of the tag, which is 46 characters — well inside the
// 128 the `digest` parameter allows. An empty digest — the node has no config file
// — has no token, and the answer is "" rather than a token for nothing: a read
// that returns no `digest` is how a caller learns the save has nothing to be
// based on.
func nodeConfigToken(encryptionKey string, clusterID uuid.UUID, node, digest string) (string, error) {
	if digest == "" {
		return "", nil
	}
	msg, err := nodeConfigTokenMessage(clusterID, node, digest)
	if err != nil {
		return "", err
	}
	tag, err := crypto.Tag(encryptionKey, nodeConfigTokenPurpose, msg)
	if err != nil {
		return "", err
	}
	return nodeConfigTokenPrefix + base64.RawURLEncoding.EncodeToString(tag), nil
}

// nodeConfigTokenMatches reports whether token is the token for this cluster,
// this node and this digest. Anything else is simply "no": a token of another
// version, one that is not base64url, one of the wrong length, Proxmox's own raw
// digest, or the empty string. None of those is an error, and none says which way
// it was wrong. The only error is the one a bad ENCRYPTION_KEY gives, which is the
// server's and not the caller's.
//
// The comparison is hmac.Equal over the whole encoded token, in constant time
// for tokens of the length a real one has. Comparing the strings would not be
// merely a style choice: == stops at the first byte that differs, and which byte
// that is leaks how much of a guess was right (TestNodeConfigTokenIsComparedWithHMACEqual).
// Comparing the encodings rather than decoding the caller's also leaves nothing to
// canonicalise: only the one spelling of a tag is accepted.
func nodeConfigTokenMatches(encryptionKey string, clusterID uuid.UUID, node, digest, token string) (bool, error) {
	want, err := nodeConfigToken(encryptionKey, clusterID, node, digest)
	if err != nil {
		return false, err
	}
	if want == "" {
		return false, nil
	}
	return hmac.Equal([]byte(token), []byte(want)), nil
}

// nodeConfigSaveDigest turns what a save sent as its `digest` into the digest
// Proxmox is to compare, or refuses the save. It is the compare-and-swap of both
// writers of the node config file, the settings and the ACME settings.
//
//   - No token: "" and no error. The save is unconditional, as it always was.
//   - A token: Proxmox is asked for the file's digest as it is NOW, the token for
//     that is minted, and the two are compared. The fresh digest is returned when
//     they match, and the caller sends it on, in Proxmox's own form, so that
//     Proxmox's check still covers the moment between this read and its write.
//   - No match, or a file with no digest to match: 409, in the words
//     mapNodeConfigError uses for Proxmox's own refusal. Nothing is written.
//
// The caller runs every refusal of its own request first (proxmox.ValidateNode…,
// with nodeConfigValidationDigest as the digest), so a request that is going to be
// refused costs no Proxmox read; and it runs this after createProxmoxClient, like
// every refusal, which is what the route sweep needs.
func nodeConfigSaveDigest(ctx context.Context, px *proxmox.Client, encryptionKey string, clusterID uuid.UUID, node, token string) (string, error) {
	if token == "" {
		return "", nil
	}
	fresh, err := px.GetNodeConfigDigest(ctx, node)
	if err != nil {
		return "", mapProxmoxError(err)
	}
	ok, err := nodeConfigTokenMatches(encryptionKey, clusterID, node, fresh, token)
	if err != nil {
		return "", fiber.NewError(fiber.StatusInternalServerError, "Failed to check the save token")
	}
	if !ok {
		return "", fiber.NewError(fiber.StatusConflict, nodeConfigChangedMessage)
	}
	return fresh, nil
}

// nodeConfigDigestPlaceholder stands in, for the pre-save validation, for the
// digest the save will carry. Proxmox's digest is PVE::NodeConfig's
// Digest::SHA::sha1_hex of the file, which is always 40 hex characters, and what
// the validation has to count is the request as Proxmox will receive it: the size
// limit is on the whole encoded body, `digest=` and its 40 characters included.
// What the caller sent is no stand-in for that — a save token is 46 characters, and
// no token at all is none — so the request is validated with this instead.
const nodeConfigDigestPlaceholder = "0000000000000000000000000000000000000000"

// nodeConfigValidationDigest is the `digest` a save's pre-save validation is run
// with: the placeholder when the caller sent a token, whatever it is, and nothing
// when it sent none, which is a save that carries no digest. With it the size
// check that precedes the re-read is exact: a body within 48 bytes of the limit —
// over it once the digest is added — is refused 413 without costing the re-read,
// whether or not its token would have matched. (The writer checks again, with the
// digest it really sends.)
func nodeConfigValidationDigest(token string) string {
	if token == "" {
		return ""
	}
	return nodeConfigDigestPlaceholder
}

// nodeOptionsForAudit is a node options write as its audit row may see it: the
// same request, without its digest. After nodeConfigSaveDigest the request's Digest
// is no longer what the caller sent but Proxmox's RAW digest of the file, which is
// the very value the save token exists to keep from callers, and the audit row is
// readable by every Viewer (view:audit). The builder does not record it
// (TestNodeOptionsAuditDetails, and the field walk that classifies Digest as
// recording nothing), and a builder that is never handed it cannot start to by a
// reclassification of that field. SetNodeOptions builds the row from this copy and
// from nothing else (TestGuard_NodeConfigAuditIsBuiltFromACopyWithoutTheDigest).
func nodeOptionsForAudit(req proxmox.NodeOptions) proxmox.NodeOptions {
	req.Digest = ""
	return req
}

// nodeACMEConfigForAudit is nodeOptionsForAudit for a node's ACME settings: the
// request SetNodeACMEConfig writes, without the raw digest that replaced the save
// token.
func nodeACMEConfigForAudit(req proxmox.NodeACMEConfig) proxmox.NodeACMEConfig {
	req.Digest = ""
	return req
}

// readNodeConfigToken is the token a read returns in its `digest` field: the
// token for the digest Proxmox just returned, or "" when it returned none. A
// failure is the server's — the encryption key — and says nothing about the
// caller's request.
func readNodeConfigToken(encryptionKey string, clusterID uuid.UUID, node, digest string) (string, error) {
	token, err := nodeConfigToken(encryptionKey, clusterID, node, digest)
	if err != nil {
		return "", fiber.NewError(fiber.StatusInternalServerError, "Failed to build the save token")
	}
	return token, nil
}
