package handlers

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// Three GET handlers used to hand a Proxmox config struct straight to the
// response writer, credential fields and all:
//
//	GET .../storage/:storage_id/config   StorageConfig{password,keyring,encryption-key}
//	GET .../sdn/ipams                    SDNIPAM{token}
//	GET .../sdn/dns                      SDNDNS{key}
//
// All three are gated on view:storage / view:network, which every built-in Viewer
// holds, so a read-only account could read storage backend passwords, Ceph
// keyrings, PBS encryption keys and SDN plugin API tokens. metric_servers.go
// had already established the fix for its own InfluxDB token — blank the
// write-only credential on the read — and these follow it.
//
// The guards below are in three layers because each catches a different
// mistake:
//
//  1. TestReadStructsStripCredentials fills every string field with a probe
//     value and checks what survives into the JSON. It fails if a strip is
//     removed, and it fails on a NEW credential-shaped field nobody has
//     classified yet — which is the failure mode per-field assertions cannot
//     see.
//  2. TestGuard_StorageConfigResponseHasOneConstructor pins the storage body
//     to its constructor, so deleting the call rather than the assignment is
//     caught too.
//  3. TestGuard_CredentialReadsAreShapedBeforeResponding requires every
//     handler that performs one of these reads to call the shaper, so a
//     handler that skips it — or a new one that never had it — is caught.
//
// Stated limitation: all three reason about THIS package, and only about the
// three structs listed in readStructs. A credential leaving by another route —
// a log line, an audit row, an error string, another package's handler — is out
// of their sight. The audit route in particular matters here: audit_log.details
// is readable by anyone with view:audit, which every Viewer has by default, and
// identity_secret_guard_test.go is the guard that covers that route for the
// identity handlers.
//
// A second limitation, worth naming because it looks like coverage: the walk
// is FLAT and string-only. readStructs lists three handler-RESPONSE structs,
// and TestReadStructsStripCredentials iterates each one's own fields and
// skips everything whose Kind is not reflect.String. A non-string field is
// still examined by NAME — it is reported if its json name looks
// credential-shaped — but its own fields are never reached, so a
// credential-bearing STRUCT held as a field is never opened, exported or not,
// on a listed struct or not. (isCredentialish("config") is false, so even the
// name check is silent here.) internal/rolling's
// failoverTarget, which holds a proxmox.ClientConfig in `config`, is out of
// scope twice over: it is not a response struct, and a struct field would be
// passed over even if it were. Field visibility is not what decides this; do
// not reach for exporting a field as a way of buying coverage here. The fix
// for that shape is redaction on the TYPE, not another classifier: see the
// ClientConfig entry below.
//
// The sweep that produced this file also looked at every other internal/proxmox
// struct with a credential-shaped field and settled each one. None needed a
// change, and none is enforced here, so the findings are recorded rather than
// re-derived:
//
//	AccessUser.Keys, AccessUserDetail.Keys   reach the client via ListUsers and
//	  GetUser. PVE documents `keys` as "Keys for two factor auth (yubico)" —
//	  Yubico public identities, not a secret. (registry_access.go describes it
//	  as SSH public keys, which is wrong but equally not a secret.)
//	AccessToken                              carries no secret by construction;
//	  Proxmox returns a token's value exactly once, at creation.
//	AccessTokenCreated.Value                 IS the secret, returned on purpose
//	  by CreateToken/UpdateToken — there is no read-back endpoint — and already
//	  kept out of the audit row.
//	TermProxyResponse.Password               never read anywhere; the sibling
//	  Ticket is sent to the browser on purpose, as noVNC's RFB password, and is
//	  logged by length only.
//	NodeSubscription.Key                     decoded by the collector, which
//	  reads Status and Level and discards the rest. No handler calls it.
//	TargetEndpoint.APIToken                  write-side only, for remote
//	  migration. The real property string now lives on PropertyString(), while
//	  String/GoString/LogValue/MarshalJSON redact every rendering that
//	  dispatches on the type. Guarded in internal/proxmox by
//	  TestGuard_TargetEndpointNeverPrintsItsToken.
//	ClientConfig.TokenSecret                 never returned by a handler — it
//	  is a constructor argument, built as a literal and passed straight to
//	  NewClient/NewPBSClient at every one of its fourteen non-test sites —
//	  including failoverTarget.config, which holds one only to hand it over. It is
//	  recorded here, and NOT added to readStructs, on purpose: with no handler
//	  response to shape there is no respond func to write, and inventing one
//	  would be a second mechanism that proves nothing. The exposure is
//	  rendering, not responding, so the fix is on the type —
//	  String/GoString/LogValue/MarshalJSON, guarded in internal/proxmox by
//	  TestGuard_ClientConfigNeverPrintsItsTokenSecret. That is also as much of
//	  the failoverTarget shape as anything can cover: `t.config` selected from
//	  one is redacted; only %v of the whole failoverTarget is not.
//	ClusterJoinInfo, NodeCertificate, NodeListEntry, StorageConfig.fingerprint
//	  carry TLS fingerprints and public key metadata — public by definition.
//	VMConfig (a bare map[string]interface{}) is returned raw by the VM and CT
//	  config handlers. PVE masks cipassword on read and sshkeys are public, but
//	  a map has no field list, so nothing of this shape can guard it.

// credentialish are the substrings that make a json field name look like it
// carries something a caller could authenticate with. Deliberately broad: a
// false positive costs one line in keptFields with a reason, while a false
// negative is a published secret.
//
// What it cannot catch, stated plainly: a credential whose field name says
// nothing. proxmox.ACMEPlugin.Data is the live example — it holds the DNS
// provider's API credentials and is blanked by hand in acme.go, and no list of
// name substrings would ever have found it. A field named for its role rather
// than its content still needs a human to notice.
var credentialish = []string{
	"password", "passwd", "token", "secret", "key", "keyring", "passphrase",
	"credential", "pubkey", "privkey", "psk", "apitoken", "ticket", "salt",
	"auth", "cert", "fingerprint",
}

func isCredentialish(jsonName string) bool {
	lower := strings.ToLower(jsonName)
	for _, needle := range credentialish {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

// readStruct is one Proxmox struct that a GET handler in this package hands to
// the response writer, together with the classification of every
// credential-shaped field on it.
type readStruct struct {
	// name is what the failure message calls the struct.
	name string
	// typ is the Proxmox struct itself, walked field by field.
	typ reflect.Type
	// respond runs the value through the handler's own response shaping and
	// returns exactly what would be marshalled. The test never re-implements
	// the stripping: if respond stops stripping, the test fails.
	respond func(reflect.Value) any
	// stripped are the json names respond must blank.
	stripped []string
	// keptFields are the credential-SHAPED names that are not credentials,
	// each with the reason it stays. A value here must still reach the body:
	// stripping it would be noise, and silently dropping it would break the
	// editor that reads it.
	keptFields map[string]string
}

func readStructs() []readStruct {
	return []readStruct{
		{
			name: "proxmox.StorageConfig",
			typ:  reflect.TypeOf(proxmox.StorageConfig{}),
			respond: func(v reflect.Value) any {
				cfg, ok := v.Interface().(proxmox.StorageConfig)
				if !ok {
					panic("readStructs: StorageConfig probe built the wrong type")
				}
				return newStorageConfigResponse(cfg)
			},
			stripped: []string{"password", "keyring", "encryption-key"},
			keptFields: map[string]string{
				"fingerprint":   "the PBS server's TLS certificate fingerprint — public, and the edit dialog shows it",
				"master-pubkey": "a PUBLIC key: PBS encrypts a copy of the backup key to it so the private half can recover it",
			},
		},
		{
			name: "proxmox.SDNIPAM",
			typ:  reflect.TypeOf(proxmox.SDNIPAM{}),
			respond: func(v reflect.Value) any {
				ipam, ok := v.Interface().(proxmox.SDNIPAM)
				if !ok {
					panic("readStructs: SDNIPAM probe built the wrong type")
				}
				return sdnIPAMsForRead([]proxmox.SDNIPAM{ipam})
			},
			stripped:   []string{"token"},
			keptFields: map[string]string{},
		},
		{
			name: "proxmox.SDNDNS",
			typ:  reflect.TypeOf(proxmox.SDNDNS{}),
			respond: func(v reflect.Value) any {
				dns, ok := v.Interface().(proxmox.SDNDNS)
				if !ok {
					panic("readStructs: SDNDNS probe built the wrong type")
				}
				return sdnDNSForRead([]proxmox.SDNDNS{dns})
			},
			stripped:   []string{"key"},
			keptFields: map[string]string{},
		},
	}
}

// jsonName is the wire name of a struct field, or "" when it is not marshalled.
func jsonName(f reflect.StructField) string {
	tag, ok := f.Tag.Lookup("json")
	if !ok {
		return f.Name
	}
	name, _, _ := strings.Cut(tag, ",")
	switch name {
	case "-":
		return ""
	case "":
		return f.Name
	}
	return name
}

// probeValue is the marker written into a field. It has to be findable in the
// marshalled body and obviously not a real credential — this repo is public.
func probeValue(structName, wireName string) string {
	return fmt.Sprintf("PROBE-%s-%s-NOT-A-REAL-VALUE", structName, wireName)
}

// TestReadStructsStripCredentials fills every string field of each read struct
// with a distinct probe, runs it through the handler's own response shaping,
// and reads the resulting JSON back.
//
// A stripped field's probe must be gone and its key with it; a kept field's
// probe must survive. A credential-shaped field that is in neither list fails
// the test by name — that is the guard against a NEW credential appearing on a
// struct these handlers return.
func TestReadStructsStripCredentials(t *testing.T) {
	structsSeen := 0

	for _, rs := range readStructs() {
		t.Run(rs.name, func(t *testing.T) {
			if rs.typ.Kind() != reflect.Struct {
				t.Fatalf("%s is a %s, not a struct — the probe cannot fill it", rs.name, rs.typ.Kind())
			}

			probe := reflect.New(rs.typ).Elem()
			credentialFields := 0
			// aliveProbes are the probes of fields that are NOT credentials.
			// At least one must reach the body, otherwise "the secret is
			// absent" is true only because nothing was marshalled at all.
			aliveProbes := map[string]string{}
			// probed records the wire names actually written, so a stripped
			// entry naming a field that no longer exists is caught. Without
			// this, deleting Keyring from StorageConfig while leaving
			// "keyring" in stripped would keep passing: the probe is never
			// written, so its absence from the body proves nothing.
			probed := map[string]bool{}

			for i := range rs.typ.NumField() {
				field := rs.typ.Field(i)
				if !field.IsExported() {
					continue
				}
				name := jsonName(field)
				if name == "" {
					continue
				}
				credential := isCredentialish(name)
				if credential {
					credentialFields++
					classified := slices.Contains(rs.stripped, name)
					if _, kept := rs.keptFields[name]; kept {
						classified = true
					}
					if !classified {
						t.Errorf("%s.%s (json %q) looks like a credential and nothing in this test says what to do with it.\n"+
							"Either blank it on the read and add %q to stripped, or, if it is not a secret "+
							"(a public key or a fingerprint is not), add it to keptFields with the reason.",
							rs.name, field.Name, name, name)
						continue
					}
				}

				if field.Type.Kind() != reflect.String {
					// A credential the probe cannot write is a credential this
					// test cannot check — say so rather than pass vacuously.
					if credential {
						t.Errorf("%s.%s (json %q) is credential-shaped but a %s, which this probe cannot fill. "+
							"Extend probeValue/this loop to cover that kind before relying on this test.",
							rs.name, field.Name, name, field.Type.Kind())
					}
					continue
				}

				value := probeValue(rs.typ.Name(), name)
				probe.Field(i).SetString(value)
				probed[name] = true
				if !credential {
					aliveProbes[name] = value
				}
			}

			if credentialFields == 0 {
				t.Fatalf("%s: examined 0 credential-shaped fields. Either the struct lost them all "+
					"(then delete this entry) or credentialish/jsonName stopped matching (then this "+
					"test has been passing without looking at anything).", rs.name)
			}
			if len(aliveProbes) == 0 {
				t.Fatalf("%s: no non-credential string field to probe with, so an absent secret proves nothing.", rs.name)
			}

			encoded, err := json.Marshal(rs.respond(probe))
			if err != nil {
				t.Fatalf("marshal %s response: %v", rs.name, err)
			}
			body := string(encoded)

			// The body has to be live before absence means anything.
			alive := false
			for _, value := range aliveProbes {
				if strings.Contains(body, value) {
					alive = true
					break
				}
			}
			if !alive {
				t.Fatalf("%s: none of the %d non-credential probes reached the body %s — "+
					"the response shaping dropped everything, so this test cannot tell a stripped "+
					"secret from an empty response.", rs.name, len(aliveProbes), body)
			}

			for _, name := range rs.stripped {
				if !probed[name] {
					t.Errorf("%s: stripped names %q, but no string field marshals under that name. "+
						"The check below would pass without testing anything — fix the name or drop it.",
						rs.name, name)
					continue
				}
				value := probeValue(rs.typ.Name(), name)
				if strings.Contains(body, value) {
					t.Errorf("%s: the %q credential reached the response body. "+
						"GET responses on these routes are readable by every Viewer.\nbody: %s",
						rs.name, name, body)
				}
				// Every stripped field is `omitempty`, so blanking must drop
				// the key outright rather than publish "" — which would read
				// as "this storage has no password set". The body is already
				// in the message above when both fire; don't print it twice.
				if strings.Contains(body, `"`+name+`":`) {
					t.Errorf("%s: the %q key is still present in the response body; it should be "+
						"omitted entirely, not blanked.", rs.name, name)
				}
			}

			for name, reason := range rs.keptFields {
				value := probeValue(rs.typ.Name(), name)
				if !strings.Contains(body, value) {
					t.Errorf("%s: %q was dropped from the response but is not a secret (%s). "+
						"Stripping it is noise and breaks the reader that needs it.\nbody: %s",
						rs.name, name, reason, body)
				}
			}
		})
		structsSeen++
	}

	if structsSeen == 0 {
		t.Fatal("readStructs() returned nothing — this test examined no struct at all")
	}
}

// parsePackageFiles parses every non-test .go file in this package.
func parsePackageFiles(t *testing.T) (*token.FileSet, []*ast.File) {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatal("parsed no package files — the AST guards below would pass without looking at anything")
	}
	return fset, files
}

// TestGuard_StorageConfigResponseHasOneConstructor keeps the credential
// stripping from being optional. Blanking inside newStorageConfigResponse only
// helps while that is the only way the response body gets built; a bare
// `storageConfigResponse{*cfg}` anywhere else reinstates the leak, and reads as
// perfectly ordinary Go.
func TestGuard_StorageConfigResponseHasOneConstructor(t *testing.T) {
	fset, files := parsePackageFiles(t)

	literals := 0
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok {
					return true
				}
				ident, ok := lit.Type.(*ast.Ident)
				if !ok || ident.Name != "storageConfigResponse" {
					return true
				}
				literals++
				if fn.Name.Name != "newStorageConfigResponse" {
					t.Errorf("%s: storageConfigResponse is built in %s, not in newStorageConfigResponse. "+
						"That construction site does not blank password/keyring/encryption-key — "+
						"build the body through the constructor.",
						fset.Position(lit.Pos()), fn.Name.Name)
				}
				return true
			})
		}
	}

	if literals == 0 {
		t.Fatal("found no storageConfigResponse composite literal at all — the constructor was renamed " +
			"or removed, and this guard has stopped watching anything")
	}
}

// credentialRead ties a Proxmox client read that returns a credential to the
// function that must shape the result before it reaches the response writer.
type credentialRead struct {
	// clientCall is the *proxmox.Client method, by bare name.
	clientCall string
	// shaper is the function in this package that blanks the credential. Any
	// handler calling clientCall must call it too.
	shaper string
	// exempt are functions that call clientCall but hand none of the struct
	// back, mapped to why. Each entry must match a real call site: a stale
	// exemption is an unguarded handler wearing a permission slip.
	exempt map[string]string
}

// TestGuard_CredentialReadsAreShapedBeforeResponding pins the other half of the
// mistake the constructor guard cannot see. Blanking inside a shaper only helps
// while every handler that performs the read calls it, and a call is easy to
// drop — or never to add in a new handler — while the function around it still
// reads as ordinary Go. In particular, this is what stops GetConfig from going
// back to `return c.JSON(*cfg)`: that leaves newStorageConfigResponse intact
// but unused, which the constructor guard would happily accept.
func TestGuard_CredentialReadsAreShapedBeforeResponding(t *testing.T) {
	reads := []credentialRead{
		{clientCall: "GetSDNIPAMs", shaper: "sdnIPAMsForRead"},
		{clientCall: "GetSDNDNSPlugins", shaper: "sdnDNSForRead"},
		{
			clientCall: "GetStorageConfig",
			shaper:     "newStorageConfigResponse",
			exempt: map[string]string{
				"DeleteImportSource": "reads cfg.Type and cfg.Content to decide whether the storage is an import source; never returns cfg",
				"EnableImportContent": "reads cfg.Content to decide whether 'import' is already set, and echoes that one " +
					"non-credential field; never returns cfg",
			},
		},
	}

	fset, files := parsePackageFiles(t)

	callers := map[string]int{}
	exemptUsed := map[string]bool{}
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			called := map[string]bool{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch f := call.Fun.(type) {
				case *ast.SelectorExpr:
					called[f.Sel.Name] = true
				case *ast.Ident:
					called[f.Name] = true
				}
				return true
			})
			for _, read := range reads {
				if !called[read.clientCall] {
					continue
				}
				callers[read.clientCall]++
				if _, ok := read.exempt[fn.Name.Name]; ok {
					exemptUsed[read.clientCall+"."+fn.Name.Name] = true
					continue
				}
				if !called[read.shaper] {
					t.Errorf("%s: %s calls %s but never %s — the credential would reach the response, "+
						"which view:storage / view:network make readable by every Viewer. Shape it, or "+
						"add %s to this read's exempt map with the reason it returns nothing sensitive.",
						fset.Position(fn.Pos()), fn.Name.Name, read.clientCall, read.shaper, fn.Name.Name)
				}
			}
		}
	}

	for _, read := range reads {
		if callers[read.clientCall] == 0 {
			t.Fatalf("found no call to %s in this package — it was renamed or moved, and this guard "+
				"has stopped watching it", read.clientCall)
		}
		for name := range read.exempt {
			if !exemptUsed[read.clientCall+"."+name] {
				t.Errorf("%s is exempted from the %s check but no function of that name calls %s. "+
					"A stale exemption silently excuses whatever takes the name next — drop it.",
					name, read.shaper, read.clientCall)
			}
		}
	}
}
