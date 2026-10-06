package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Guards one bug class: a type holds a credential in a field AND declares a method that renders itself, so
// %v, %#v, a slog attribute or json.Marshal prints the secret (each found by a human, e.g. proxmox.
// TargetEndpoint, ClientConfig, ssh.Config). Every type in a non-test file under internal/ with a
// credential-shaped field and any of String, GoString, Error, LogValue, MarshalJSON, MarshalText or Format
// must be in credentialRenderers with the reviewed renderings and a reason. It lives in package handlers to
// reuse credentialish and isNestedCheckout. Limits, none closed: it keys on the field NAME (ACMEPlugin.Data
// and AccessDomain.TFA need a human); it needs a rendering method (183 other types hold such a field, a
// finding set nobody reads); holding is followed one level, by VALUE in a NAMED unexported field, the only
// shape that leaks (verified: pointers print as addresses, embedded inners are promoted and redact); it
// walks internal/ only; and `type Foo Bar` is invisible.
var credentialRenderMethods = []string{
	"String", "GoString", "Error", "LogValue", "MarshalJSON", "MarshalText", "Format",
}

// credentialRendererNote is the record of somebody having looked at one type.
type credentialRendererNote struct {
	// redacts says this entry's claim is "the renderings hide the credential", as opposed to "the field is not
	// a credential at all". A redacting rendering MUST be on a value receiver: fmt and encoding/json skip a
	// pointer-receiver method on a value they cannot address, and these types are passed by value, so
	// `func (c *Config) String()` compiles, lints clean and redacts nothing (made three times:
	// authResponse, TargetEndpoint, ClientConfig). A "not a secret" entry sets this false and may use any
	// receiver (TokenExistsError and HostKeyMismatchError declare Error on a pointer, idiomatic for an error
	// type and harmless when there is nothing to hide).
	redacts bool
	// renderings are the credentialRenderMethods that were reviewed, and the guard requires the type to
	// declare exactly these. Pinning the SET, not just the type, is what makes an entry keep earning itself:
	// adding MarshalText or Format to a listed type is the bug class again, written by somebody who did not
	// necessarily read the other four, and with membership alone the guard would wave it through while the
	// reason string went quietly false. Empty means the type declares none, valid only for an entry listed
	// for the transitive reason.
	renderings []string
	// reason is why this type is allowed to render itself while holding a
	// credential-shaped field. Either it redacts — name the test that pins
	// that, so removing the redaction fails something — or the field is not a
	// credential, in which case say why. A public key and a TLS fingerprint are
	// not secrets.
	reason string
}

// credentialRenderers is every type under internal/ that this guard has
// classified, keyed by repository-relative directory plus type name so that two
// packages with the same name cannot collide.
var credentialRenderers = map[string]credentialRendererNote{
	"internal/api/handlers.authResponse": {
		redacts:    true,
		renderings: []string{"GoString", "LogValue", "MarshalJSON", "String"},
		reason: "redacts; access_token/refresh_token are dropped by all four renderings — " +
			"see TestGuard_AuthResponseNeverPrintsEitherToken",
	},
	"internal/api/handlers.logoutRequest": {
		redacts:    true,
		renderings: []string{"GoString", "LogValue", "MarshalJSON", "String"},
		reason: "redacts; the refresh token is dropped by all four renderings — " +
			"see TestGuard_LogoutRequestNeverPrintsTheRefreshToken",
	},
	"internal/api/handlers.bootstrapRequest": {
		redacts:    true,
		renderings: []string{"GoString", "LogValue", "MarshalJSON", "String"},
		reason: "redacts; password and OTP are dropped by all four renderings — " +
			"see TestGuard_BootstrapRequestGoStringRedactsThePassword",
	},
	"internal/api/handlers.clusterCredential": {
		redacts:    true,
		renderings: []string{"GoString", "LogValue", "MarshalJSON", "String"},
		reason: "redacts; the plaintext Secret is dropped by all four renderings — " +
			"see TestGuard_ClusterCredentialNeverPrintsItsSecret",
	},
	"internal/proxmox.ClientConfig": {
		redacts:    true,
		renderings: []string{"GoString", "LogValue", "MarshalJSON", "String"},
		reason: "redacts; TokenSecret is dropped by all four renderings — " +
			"see TestGuard_ClientConfigNeverPrintsItsTokenSecret",
	},
	"internal/proxmox.TargetEndpoint": {
		redacts:    true,
		renderings: []string{"GoString", "LogValue", "MarshalJSON", "String"},
		reason: "redacts; APIToken is dropped by all four renderings and the real property string " +
			"lives on PropertyString — see TestGuard_TargetEndpointNeverPrintsItsToken",
	},
	"internal/ssh.Config": {
		redacts:    true,
		renderings: []string{"GoString", "LogValue", "MarshalJSON", "String"},
		reason: "redacts; Password and PrivateKey are dropped by all four renderings — " +
			"see TestGuard_SSHConfigNeverPrintsItsCredentials",
	},
	"internal/veeam.tokenResponse": {
		redacts:    true,
		renderings: []string{"GoString", "LogValue", "MarshalJSON", "String"},
		reason: "redacts; access and refresh tokens are dropped by all four renderings — " +
			"see TestGuard_VeeamTokenResponseNeverPrintsItsTokens",
	},

	// Not secrets. These match credentialish on fingerprint/pubkey/token-name
	// substrings and are listed so the match is answered rather than re-derived.
	"internal/proxmox.NodeCertificate": {
		renderings: []string{"MarshalJSON"},
		reason: "not a secret; Fingerprint, PublicKeyBits, PublicKeyType and PEM are the " +
			"certificate's public half, and MarshalJSON only reshapes the SAN list",
	},
	"internal/proxmox.TokenExistsError": {
		renderings: []string{"Error"},
		reason: "not a secret; TokenName is an API token's NAME, and Error prints the user!token " +
			"identifier — Proxmox returns a token's value once, at creation, and this error exists " +
			"precisely because no secret is available",
	},
	"internal/ssh.HostKeyMismatchError": {
		renderings: []string{"Error"},
		reason: "not a secret; the expected and presented host key fingerprints and the presented " +
			"public key are public by definition, and printing them is the point of the error",
	},

	// Transitive: held by value in an unexported field, so the inner redaction
	// is unreachable. Accepted, not fixed.
	//
	// This entry is load-bearing beyond its own type. failoverTarget reaches
	// scope ONLY through the propagation rule, so if that rule ever stops
	// working — a broken import resolver, a mis-derived key — this entry goes
	// stale and the staleness check fires. Deleting the line would quietly
	// un-test the whole transitive half.
	"internal/rolling.failoverTarget": {
		renderings: nil,
		reason: "ACCEPTED RESIDUAL, not redacted: holds a proxmox.ClientConfig by value in the " +
			"unexported field `config`, so %v of the whole failoverTarget prints the token secret. " +
			"Exporting the field would close it and is deliberately not done — see the type's own " +
			"comment in orchestrator.go. So would holding it by pointer, equally not done: the config " +
			"is a small value built per target and handed to NewClient by value, so a pointer buys " +
			"aliasing and an indirection for nothing. Call sites name the pieces (t.name, " +
			"t.config.BaseURL) instead",
	},
}

// credentialRenderType is one struct type declared under internal/.
type credentialRenderType struct {
	// key is "<repo-relative dir>.<TypeName>", e.g. "internal/proxmox.ClientConfig".
	key string
	// pos is the file:line of the type declaration, for the failure message.
	pos string
	// credFields are the field names isCredentialish matches.
	credFields []string
	// renderers are the credentialRenderMethods this type declares, on either a
	// value or a pointer receiver, sorted.
	renderers []string
	// pointerReceivers holds the subset of renderers declared on a pointer
	// receiver. For a redacting type that is a bug, not a detail: see
	// credentialRendererNote.redacts.
	pointerReceivers map[string]bool
	// heldByValue maps the key of a declared type held BY VALUE in a NAMED
	// UNEXPORTED field to that field's name. Slices, arrays and map values are
	// unwrapped; pointers are not, because a pointer below the top level prints
	// as an address and leaks nothing, and embedded fields are skipped, because
	// method promotion redacts them at depth 0. See limitation 3.
	heldByValue map[string]string
}

// credentialRenderScan is what one walk of internal/ produced. Every count here
// but one is read by a floor, so that a walk which quietly stops finding
// anything fails instead of passing.
//
// filesParsed is the exception and is DIAGNOSTIC ONLY: its floor was deleted
// because it could never fire — it and pkgNameByDir advance over the same
// slice, so the pkgNameByDir fatal always won. It is kept because that fatal's
// message quotes it, and it is called out here rather than left looking like a
// fourth safety net. An unread counter in this file is the same shape as the
// un-failable check the file exists to prevent.
type credentialRenderScan struct {
	types          map[string]*credentialRenderType
	filesParsed    int // diagnostic only — see above; no floor reads this
	typesExamined  int
	renderersFound int
	// crossPkgFields counts unexported field types resolved through an import,
	// which is the machinery the transitive half depends on.
	crossPkgFields int
	// guardTests are the names of every Test function declared in a _test.go
	// file under internal/, so a `see TestGuard_…` citation can be checked.
	guardTests map[string]bool
}

// TestGuard_CredentialBearingTypesRedactTheirRenderings fails when a type under internal/ holds a
// credential-shaped field, renders itself, and nobody has said which of those two facts is wrong. It
// also fails on an allow-list entry that has stopped being true: the type was deleted or stopped
// rendering itself (the redaction removed), the credential field is gone, it grew an unreviewed
// rendering, a redacting rendering moved to a pointer receiver, the reason is blank, or the test it
// cites does not exist. An entry nobody has to keep true silently excuses whatever takes the name next.
func TestGuard_CredentialBearingTypesRedactTheirRenderings(t *testing.T) {
	t.Parallel()

	scan := scanInternalForCredentialRenderers(t)

	// Non-vacuity: each of these has failed silently in this repo before (a walk that finds no files, a
	// classifier that matches nothing, a name resolver that resolves nothing). There is deliberately no
	// "parsed no files" floor: it would be dominated, since filesParsed and pkgNameByDir advance in lockstep
	// and the scanner already fatals when pkgNameByDir is empty; an advertised floor that cannot fire is
	// the one defect this file cannot afford.
	if scan.typesExamined == 0 {
		t.Fatal("examined 0 struct types under internal/ — the parse or the walk is broken, " +
			"and this guard would report no findings for that reason alone")
	}
	if scan.renderersFound == 0 {
		t.Fatalf("found no type declaring any of %v under internal/ — either every rendering "+
			"method in the tree was deleted, or the method collector stopped matching. "+
			"Nothing can be in scope, so a pass proves nothing.", credentialRenderMethods)
	}
	if scan.crossPkgFields == 0 {
		t.Fatal("resolved no unexported field type through an import — the transitive half of this " +
			"guard (a credential held by value in an unexported field of another package's type) " +
			"cannot fire, so internal/rolling.failoverTarget and anything like it are invisible")
	}
	if len(scan.guardTests) == 0 {
		t.Fatal("found no Test functions in any _test.go file under internal/ — the `see TestGuard_…` " +
			"citations below could not be checked, and every one of them would pass unread")
	}

	// A type is directly in scope when it holds a credential-shaped field and
	// renders itself.
	direct := map[string]bool{}
	for key, typ := range scan.types {
		if len(typ.credFields) > 0 && len(typ.renderers) > 0 {
			direct[key] = true
		}
	}
	// No magic minimum here on purpose: the allow-list is the coverage canary.
	// Every one of its entries must still be classified in scope, so a
	// classifier that narrows — isCredentialish losing "fingerprint", say —
	// fails through the staleness check below and names which type it lost.
	if len(direct) == 0 {
		t.Fatal("no type under internal/ both holds a credential-shaped field and renders itself. " +
			"Eleven did when this guard was written; isCredentialish or the method collector has " +
			"stopped matching, and every finding below would be missing for that reason")
	}

	inScope := map[string]bool{}
	maps.Copy(inScope, direct)

	for _, key := range slices.Sorted(maps.Keys(scan.types)) {
		typ := scan.types[key]
		if direct[key] {
			if _, allowed := credentialRenderers[key]; allowed {
				continue
			}
			t.Errorf("%s: %s declares %v and holds the credential-shaped field(s) %v, and is not in "+
				"credentialRenderers.\n"+
				"\t%%v, %%#v, a slog attribute or json.Marshal of this value dispatches to that method "+
				"and prints whatever it renders.\n"+
				"\tEither redact every rendering — String, GoString, LogValue and MarshalJSON together, "+
				"on VALUE receivers, since fmt skips a pointer method on an unaddressable value — and "+
				"list the type here with the renderings you reviewed and the test that pins them;\n"+
				"\tor list it here with the reason the field is not a secret (a public key or a TLS "+
				"fingerprint is not).",
				typ.pos, credentialRenderShortName(key), typ.renderers, typ.credFields)
			continue
		}

		// One level of propagation. Unexported AND by value: see limitation 3
		// for why every other combination is safe.
		for _, heldKey := range slices.Sorted(maps.Keys(typ.heldByValue)) {
			if !direct[heldKey] {
				continue
			}
			inScope[key] = true
			if _, allowed := credentialRenderers[key]; allowed {
				continue
			}
			t.Errorf("%s: %s holds %s by value in the unexported field %q, and is not in "+
				"credentialRenderers.\n"+
				"\t%s redacts itself, but fmt cannot call a method through an unexported field "+
				"(reflect.Value.CanInterface is false), so %%v, %%+v and %%#v of the OUTER value print "+
				"the inner struct's raw fields, credential included.\n"+
				"\tExport the field, hold it by pointer, give this type its own redacting renderings, "+
				"or list it here with the reason the exposure is accepted.",
				typ.pos, credentialRenderShortName(key), credentialRenderShortName(heldKey),
				typ.heldByValue[heldKey], credentialRenderShortName(heldKey))
		}
	}

	// The allow-list has to stay true, or it stops being a record of anyone
	// having looked.
	for _, key := range slices.Sorted(maps.Keys(credentialRenderers)) {
		note := credentialRenderers[key]
		typ, found := scan.types[key]
		if !found {
			t.Errorf("credentialRenderers lists %q, but no struct type of that name is declared under "+
				"internal/. It was renamed, moved or deleted. A stale entry excuses whatever takes the "+
				"name next — update the key or drop the line.", key)
			continue
		}

		if strings.TrimSpace(note.reason) == "" {
			t.Errorf("%s: credentialRenderers lists %s with an empty reason. The entry is the record "+
				"that somebody looked; without the reason it records nothing.",
				typ.pos, credentialRenderShortName(key))
		}
		cited := credentialRenderCitedTests(note.reason)
		for _, name := range cited {
			if !scan.guardTests[name] {
				t.Errorf("%s: credentialRenderers says %s is pinned by %s, but no test of that name is "+
					"declared under internal/. The citation is the only thing tying this entry to a "+
					"test that would fail if the redaction were removed — fix the name, or say what "+
					"does pin it.", typ.pos, credentialRenderShortName(key), name)
			}
		}

		// redacts is DERIVED, not trusted. It gates the value-receiver check
		// below, so an entry that quietly sets it false opts out of the one
		// property that decides whether a redaction works at all — and false
		// is the zero value, so "the author forgot" and "the author decided"
		// look identical in the source.
		//
		// The tell is already in the reason: an entry claiming redaction cites
		// the TestGuard_ that proves it, and an entry claiming the type holds
		// no secret cites none. That invariant holds for every entry today, so
		// assert it rather than letting the flag stand on its own.
		if wantRedacts := len(cited) > 0; note.redacts != wantRedacts {
			t.Errorf("%s: credentialRenderers marks %s redacts=%v but its reason cites %d TestGuard_ "+
				"test(s). A redacting entry cites the test that proves it; a holds-no-secret entry "+
				"cites none. Make the flag and the reason agree — and note that redacts=false "+
				"disables the value-receiver check, so setting it to quiet this error hides the "+
				"defect rather than fixing it.",
				typ.pos, credentialRenderShortName(key), note.redacts, len(cited))
		}

		// The reviewed set, not just the type. A rendering that appeared since
		// is a rendering nobody checked.
		reviewed := slices.Clone(note.renderings)
		slices.Sort(reviewed)
		if !slices.Equal(typ.renderers, reviewed) {
			t.Errorf("%s: credentialRenderers records %s as declaring %v, but it declares %v.\n"+
				"\tA rendering that is present and unreviewed is this guard's whole bug class: a new "+
				"way for the value to print, written without necessarily reading the others. A "+
				"rendering that is recorded and absent is a redaction that was removed.\n"+
				"\tCheck what the actual set does with the credential, then update the entry.",
				typ.pos, credentialRenderShortName(key), reviewed, typ.renderers)
		}

		// A redaction on a pointer receiver is not a redaction. fmt and
		// encoding/json skip it for a value they cannot address, and every one
		// of these types is passed by value.
		if note.redacts {
			for _, method := range typ.renderers {
				if !typ.pointerReceivers[method] {
					continue
				}
				t.Errorf("%s: credentialRenderers says %s redacts, but %s is declared on a POINTER "+
					"receiver.\n"+
					"\tfmt and encoding/json skip a pointer-receiver method on a value they cannot "+
					"address, and this type is passed around by value — so %%v, %%#v, json.Marshal and "+
					"slog print the raw fields and the redaction never runs. It compiles, it lints "+
					"clean, and it does nothing.\n"+
					"\tMove it to a value receiver. That mistake was made three times in the sweep "+
					"this guard came out of.",
					typ.pos, credentialRenderShortName(key), method)
			}
		}

		if inScope[key] {
			continue
		}
		switch {
		case len(typ.renderers) == 0 && len(typ.credFields) == 0:
			// Only a transitive entry can look like this: it was listed for
			// what it HOLDS, and it holds nothing classified any more.
			t.Errorf("%s: credentialRenderers lists %s, which declares no rendering and has no "+
				"credential-shaped field — so it was listed for what it holds. It no longer holds a "+
				"classified type by value in an unexported field.\n"+
				"\tEither the held type stopped being classified (check its own entry), or the field "+
				"changed shape, or the propagation rule stopped resolving it. Work out which before "+
				"dropping the entry.", typ.pos, credentialRenderShortName(key))
		case len(typ.renderers) == 0:
			t.Errorf("%s: credentialRenderers lists %s, but it no longer declares any of %v.\n"+
				"\tIf the redacting String/GoString/LogValue/MarshalJSON were removed, that is the "+
				"regression this guard exists for: %%v now prints the raw fields again. If the type "+
				"genuinely stopped rendering itself, drop the entry.",
				typ.pos, credentialRenderShortName(key), credentialRenderMethods)
		case len(typ.credFields) == 0:
			t.Errorf("%s: credentialRenderers lists %s, but it no longer has a credential-shaped field "+
				"and holds no classified type by value in an unexported one. Nothing here is guarded "+
				"any more — drop the entry, or check whether the credential moved to a field whose "+
				"name says nothing (see limitation 1 in the header).",
				typ.pos, credentialRenderShortName(key))
		default:
			// Unreachable while `direct` means "has a credential field AND
			// renders itself": the two cases above are its exact negation. It
			// is here so that widening the scope rule without revisiting this
			// switch reports the drift instead of silently accepting the entry.
			t.Errorf("%s: credentialRenderers lists %s, and this guard classifies it as neither in "+
				"scope nor stale. That combination should be impossible — the scope rule and this "+
				"staleness check have drifted apart. Fix the guard, not the entry.",
				typ.pos, credentialRenderShortName(key))
		}
	}
}

// credentialRenderCitedTests pulls every "TestGuard_Something" out of a reason
// string, so the citation can be held to naming a test that exists.
func credentialRenderCitedTests(reason string) []string {
	const marker = "TestGuard_"
	var names []string
	for rest := reason; ; {
		i := strings.Index(rest, marker)
		if i < 0 {
			return names
		}
		rest = rest[i:]
		end := len(marker)
		for end < len(rest) && (rest[end] == '_' ||
			(rest[end] >= '0' && rest[end] <= '9') ||
			(rest[end] >= 'a' && rest[end] <= 'z') ||
			(rest[end] >= 'A' && rest[end] <= 'Z')) {
			end++
		}
		names = append(names, rest[:end])
		rest = rest[end:]
	}
}

// credentialRenderShortName turns "internal/proxmox.ClientConfig" into
// "proxmox.ClientConfig". The split is on the LAST dot: a Go type name cannot
// contain one, but a directory name can.
func credentialRenderShortName(key string) string {
	dot := strings.LastIndex(key, ".")
	if dot < 0 {
		return key
	}
	dir, name := key[:dot], key[dot+1:]
	return dir[strings.LastIndex(dir, "/")+1:] + "." + name
}

// scanInternalForCredentialRenderers parses every Go file under internal/ and
// records, per struct type declared in a non-test file, its credential-shaped
// fields, the rendering methods declared on it anywhere in its package, and the
// declared types it holds by value in unexported fields. Test files contribute
// only their Test function names, for checking the allow-list's citations.
func scanInternalForCredentialRenderers(t *testing.T) credentialRenderScan {
	t.Helper()

	modulePrefix := credentialRenderModulePath(t)
	sources, tests := credentialRenderSourceFiles(t)

	scan := credentialRenderScan{
		types:      map[string]*credentialRenderType{},
		guardTests: map[string]bool{},
	}
	fset := guardFset

	// Pass one: parse, and learn what each directory's package is CALLED. A
	// Go package's name need not match its directory — internal/db/generated
	// is `package db` — so an unaliased import's local name cannot be guessed
	// from the path. Guessing it wrong is a silent miss: every field type in
	// that package resolves to nothing and the transitive half quietly skips
	// it.
	type parsedFile struct {
		dir  string
		file *ast.File
	}
	files := make([]*ast.File, len(sources))
	if err := guardEach(sources, func(i int, path string) (err error) {
		files[i], err = guardParsed(path)
		return err
	}); err != nil {
		t.Fatalf("parse internal/: %v", err)
	}
	parsed := make([]parsedFile, 0, len(sources))
	pkgNameByDir := map[string]string{}
	for i, path := range sources {
		dir := credentialRenderDir(t, path)
		pkgNameByDir[dir] = files[i].Name.Name
		parsed = append(parsed, parsedFile{dir: dir, file: files[i]})
		scan.filesParsed++
	}
	if len(pkgNameByDir) == 0 {
		t.Fatal("learned no package names under internal/ — no import could be resolved to a " +
			"directory, so nothing the transitive half depends on can work")
	}

	// Methods are collected separately: a method may be declared in a different
	// file of the same package than its type.
	renderersByType := map[string][]string{}
	pointerReceivers := map[string][]string{}

	// Pass two: classify.
	for _, pf := range parsed {
		file, dir := pf.file, pf.dir
		imports := credentialRenderImports(file, modulePrefix, pkgNameByDir)

		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				if d.Tok != token.TYPE {
					continue
				}
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					st, ok := ts.Type.(*ast.StructType)
					if !ok {
						continue
					}
					scan.typesExamined++
					typ := &credentialRenderType{
						key:              dir + "." + ts.Name.Name,
						pos:              fset.Position(ts.Pos()).String(),
						heldByValue:      map[string]string{},
						pointerReceivers: map[string]bool{},
					}
					for _, field := range st.Fields.List {
						// An embedded field contributes its type's name to the
						// credential-name check, but never to the propagation:
						// promotion puts the inner's renderings in this type's
						// method set, so fmt redacts at depth 0. See limitation 3.
						embedded := len(field.Names) == 0
						for _, name := range credentialRenderFieldNames(field) {
							if isCredentialish(name) {
								typ.credFields = append(typ.credFields, name)
							}
							if embedded || token.IsExported(name) {
								continue
							}
							key, crossPkg := credentialRenderHeldTypeKey(field.Type, dir, imports)
							if key == "" {
								continue
							}
							if crossPkg {
								scan.crossPkgFields++
							}
							typ.heldByValue[key] = name
						}
					}
					scan.types[typ.key] = typ
				}
			case *ast.FuncDecl:
				if d.Recv == nil || len(d.Recv.List) != 1 {
					continue
				}
				if !slices.Contains(credentialRenderMethods, d.Name.Name) {
					continue
				}
				recv, byPointer := credentialRenderReceiverName(d.Recv.List[0].Type)
				if recv == "" {
					continue
				}
				scan.renderersFound++
				key := dir + "." + recv
				renderersByType[key] = append(renderersByType[key], d.Name.Name)
				if byPointer {
					pointerReceivers[key] = append(pointerReceivers[key], d.Name.Name)
				}
			}
		}
	}

	for key, methods := range renderersByType {
		typ, ok := scan.types[key]
		if !ok {
			// A rendering method on a named type that is not a struct literal.
			// Today that is exactly proxmox.FlexString (a string) and
			// guesttools.taskPresence (an int), neither of which has fields at
			// all. It would ALSO be a defined type with a struct underlying
			// type — `type Foo Bar` — which does have Bar's fields and is
			// simply invisible to this scan; see limitation 5.
			continue
		}
		slices.Sort(methods)
		typ.renderers = methods
		for _, method := range pointerReceivers[key] {
			typ.pointerReceivers[method] = true
		}
	}

	// Test files, for their Test function names only. Nothing declared in one
	// is in scope; a fixture that prints its own fake secret is not a leak.
	// Parsed here and not kept: nothing else reads them.
	testFset := token.NewFileSet()
	testNames := make([][]string, len(tests))
	if err := guardEach(tests, func(i int, path string) error {
		file, err := parser.ParseFile(testFset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Test") {
				testNames[i] = append(testNames[i], fn.Name.Name)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("parse internal/ tests: %v", err)
	}
	for _, names := range testNames {
		for _, name := range names {
			scan.guardTests[name] = true
		}
	}

	return scan
}

// credentialRenderDir is path's directory, relative to the repository root and
// slash-separated, which is the first half of every key in this guard.
func credentialRenderDir(t *testing.T, path string) string {
	t.Helper()

	rel, err := filepath.Rel(filepath.Clean(repoRoot), path)
	if err != nil {
		t.Fatalf("relativise %s: %v", path, err)
	}
	return filepath.ToSlash(filepath.Dir(rel))
}

// credentialRenderModulePath reads the module path out of go.mod, so an import
// can be turned back into a repository-relative directory.
func credentialRenderModulePath(t *testing.T) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(repoRoot, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if path, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(path) + "/"
		}
	}
	t.Fatal("go.mod declares no module path — no import could be resolved, and the transitive " +
		"half of this guard would be silently dead")
	return ""
}

// credentialRenderSourceFiles returns the Go files under internal/, split into non-test sources (which
// are classified) and test files (which contribute only their Test function names). The walk is rooted
// at internal/ rather than repoRoot, which keeps stale agent worktrees (.claude/worktrees/<name>/, each
// with a full copy of the tree) out structurally, not by a filter someone can drop. The skips inside are
// belt and braces: isNestedCheckout (scope_params_guard_test.go) catches a worktree or submodule placed
// under internal/ itself (a .git FILE, invisible to a name-based skip), and the name switch a vendored
// copy. This is deliberately not goSourceFiles, which skips internal/db/generated; the generated models
// are IN scope here, since a generator that starts emitting a String() is what this guard should notice.
func credentialRenderSourceFiles(t *testing.T) (sources, tests []string) {
	t.Helper()

	root := filepath.Join(repoRoot, "internal")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "vendor", "node_modules", ".git", ".claude":
				return filepath.SkipDir
			}
			if filepath.Clean(path) != filepath.Clean(root) && isNestedCheckout(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			tests = append(tests, path)
		} else {
			sources = append(sources, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return sources, tests
}

// credentialRenderImports maps each in-module import's local name to its
// repository-relative directory.
//
// An aliased import says its own local name. An unaliased one is referred to by
// the imported package's NAME, which pkgNameByDir supplies — it is not always
// the last path segment (internal/db/generated is `package db`). For a
// directory outside the walk, and therefore absent from the map, the basename
// is the only thing left to guess with; nothing there can be in scope anyway,
// since scope is limited to types declared under internal/, so a wrong guess
// can only miss, never mis-attribute.
func credentialRenderImports(file *ast.File, modulePrefix string, pkgNameByDir map[string]string) map[string]string {
	imports := map[string]string{}
	for _, spec := range file.Imports {
		path := strings.Trim(spec.Path.Value, `"`)
		dir, ok := strings.CutPrefix(path, modulePrefix)
		if !ok {
			continue
		}
		local, known := pkgNameByDir[dir]
		if !known {
			local = dir[strings.LastIndex(dir, "/")+1:]
		}
		if spec.Name != nil {
			if spec.Name.Name == "_" || spec.Name.Name == "." {
				// A blank import declares nothing; a dot import would put the
				// package's names in this file's scope, which this resolver
				// does not model. Neither appears in internal/ today.
				continue
			}
			local = spec.Name.Name
		}
		imports[local] = dir
	}
	return imports
}

// credentialRenderFieldNames returns the names a struct field contributes. An
// embedded field contributes the embedded type's name, which is also what
// decides whether it is exported.
func credentialRenderFieldNames(field *ast.Field) []string {
	if len(field.Names) == 0 {
		if name := credentialRenderEmbeddedName(field.Type); name != "" {
			return []string{name}
		}
		return nil
	}
	names := make([]string, 0, len(field.Names))
	for _, ident := range field.Names {
		names = append(names, ident.Name)
	}
	return names
}

func credentialRenderEmbeddedName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return credentialRenderEmbeddedName(e.X)
	case *ast.SelectorExpr:
		return e.Sel.Name
	case *ast.IndexExpr:
		return credentialRenderEmbeddedName(e.X)
	case *ast.IndexListExpr:
		return credentialRenderEmbeddedName(e.X)
	}
	return ""
}

// credentialRenderHeldTypeKey resolves a field's type expression to a
// "<dir>.<TypeName>" key IF the value itself is stored in the field, reporting
// whether the resolution went through an import.
//
// Slices, arrays and map values are unwrapped: fmt prints their elements
// inline, so a credential in one leaks exactly as a direct field would.
// Pointers are NOT unwrapped, and that is the point rather than an omission —
// below the top level fmt prints a pointer as its hex address, so nothing in
// the pointee is rendered. Interfaces, channels, function types and map keys
// resolve to nothing; see limitation 3.
func credentialRenderHeldTypeKey(expr ast.Expr, dir string, imports map[string]string) (key string, crossPkg bool) {
	switch e := expr.(type) {
	case *ast.Ident:
		return dir + "." + e.Name, false
	case *ast.ArrayType:
		return credentialRenderHeldTypeKey(e.Elt, dir, imports)
	case *ast.MapType:
		return credentialRenderHeldTypeKey(e.Value, dir, imports)
	case *ast.IndexExpr:
		return credentialRenderHeldTypeKey(e.X, dir, imports)
	case *ast.IndexListExpr:
		return credentialRenderHeldTypeKey(e.X, dir, imports)
	case *ast.SelectorExpr:
		pkg, ok := e.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		target, ok := imports[pkg.Name]
		if !ok {
			return "", false
		}
		return target + "." + e.Sel.Name, true
	}
	return "", false
}

// credentialRenderReceiverName returns the receiver's type name and whether the
// method is declared on a POINTER receiver.
//
// The kind is not bookkeeping. A rendering on a pointer receiver is skipped by
// fmt and encoding/json for a value they cannot address, so it redacts nothing
// for a type that is passed by value — which all of these are. Stripping the
// star and forgetting it was the gap that let `func (c *Config) String()` pass
// as a fix three times.
func credentialRenderReceiverName(expr ast.Expr) (name string, byPointer bool) {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name, false
	case *ast.StarExpr:
		inner, _ := credentialRenderReceiverName(e.X)
		return inner, true
	case *ast.IndexExpr:
		return credentialRenderReceiverName(e.X)
	case *ast.IndexListExpr:
		return credentialRenderReceiverName(e.X)
	}
	return "", false
}
