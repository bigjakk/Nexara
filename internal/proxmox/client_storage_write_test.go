package proxmox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// storageWriteCanary is the key material in every fixture key below: a token no
// other code path produces, so finding it anywhere is finding the key. It is
// letters and hyphens only, which neither slog handler, fmt's %q nor
// encoding/json escapes — a search for it cannot miss because a rendering
// quoted it differently.
const storageWriteCanary = "CANARY-pbs-key-material-not-a-real-key"

// storageWriteKey is a key file in the shape `proxmox-backup-client key create
// --kdf none` writes — the shape PBSPlugin reads back from
// /etc/pve/priv/storage/<storage>.enc and returns. The fingerprint is synthetic.
func storageWriteKey(material string) string {
	return `{"kdf":null,"created":"2026-01-01T00:00:00+00:00","modified":"2026-01-01T00:00:00+00:00",` +
		`"data":"` + material + `","fingerprint":"aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99:` +
		`aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99"}`
}

// storageWriteCall is one request the stand-in Proxmox received.
type storageWriteCall struct {
	method string
	path   string
	form   url.Values
}

// storageWriteServer is a stand-in Proxmox that records every storage write and
// answers with a fixed body.
type storageWriteServer struct {
	mu    sync.Mutex
	calls []storageWriteCall
}

func (s *storageWriteServer) recorded() []storageWriteCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]storageWriteCall(nil), s.calls...)
}

func newStorageWriteServer(t *testing.T, answer string) (*Client, *storageWriteServer) {
	t.Helper()
	rec := &storageWriteServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, err := url.ParseQuery(string(body))
		if err != nil {
			t.Errorf("the request body is not a form: %v", err)
		}
		rec.mu.Lock()
		rec.calls = append(rec.calls, storageWriteCall{method: r.Method, path: r.URL.Path, form: form})
		rec.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(srv.Close)
	return newTestClient(t, srv.URL), rec
}

// TestStorageWrite_HandsBackOnlyAKeyProxmoxWasAskedToGenerate drives
// CreateStorage and UpdateStorage against answers shaped like the ones
// pve-storage's API2/Storage/Config.pm create and update return: {storage,
// type, config}, where config is whatever PBSPlugin's add or update hook
// returned.
//
// The load-bearing case is the supplied key. PBSPlugin echoes a caller's own
// key back as config.encryption-key (`$res->{'encryption-key'} =
// $encryption_key`), so the answer to a supplied key is indistinguishable
// from the answer to autogen. Its precondition asserts the answer really does
// carry the key; without that, "the result is empty" would pass for a server
// that never echoed anything.
func TestStorageWrite_HandsBackOnlyAKeyProxmoxWasAskedToGenerate(t *testing.T) {
	generated := storageWriteKey(storageWriteCanary + "-generated")
	supplied := storageWriteKey(storageWriteCanary + "-supplied")

	answerWithKey := func(key string) string {
		raw, _ := json.Marshal(map[string]any{"data": map[string]any{
			"storage": "store01", "type": "pbs", "config": map[string]string{"encryption-key": key},
		}})
		return string(raw)
	}

	tests := []struct {
		name   string
		sent   string // encryption-key in the request; "" sends none
		answer string
		want   string
	}{
		{"autogen, and Proxmox returned the key", PBSEncryptionKeyAutogen, answerWithKey(generated), generated},
		{"a supplied key is echoed back and dropped", supplied, answerWithKey(supplied), ""},
		{"no key asked for", "", `{"data":{"storage":"store01","type":"pbs"}}`, ""},
		// PBSPlugin's update hook returns {} when the key is untouched, and
		// Config.pm then answers config:{} — Perl treats the empty hash as true.
		{"no key asked for, empty config", "", `{"data":{"storage":"store01","type":"pbs","config":{}}}`, ""},
		// The storage has been written by the time any of these arrive, so
		// none of them may fail the call: each reads as "generated, but not
		// delivered", which the caller that sent autogen has to surface.
		{"autogen, but the answer has no config", PBSEncryptionKeyAutogen, `{"data":{"storage":"store01","type":"pbs"}}`, ""},
		{"autogen, but the answer is null", PBSEncryptionKeyAutogen, `{"data":null}`, ""},
		{"autogen, but the answer is not an object", PBSEncryptionKeyAutogen, `{"data":"unexpected"}`, ""},
	}

	for _, tt := range tests {
		for _, write := range []string{"create", "update"} {
			t.Run(write+"/"+tt.name, func(t *testing.T) {
				if tt.sent == supplied && !strings.Contains(tt.answer, storageWriteCanary+"-supplied") {
					t.Fatal("precondition: the answer to a supplied key must echo it, or dropping the echo is untested")
				}
				client, rec := newStorageWriteServer(t, tt.answer)

				params := url.Values{}
				if tt.sent != "" {
					params.Set("encryption-key", tt.sent)
				}
				var (
					result StorageWriteResult
					err    error
				)
				if write == "create" {
					params.Set("storage", "store01")
					params.Set("type", "pbs")
					result, err = client.CreateStorage(context.Background(), params)
				} else {
					result, err = client.UpdateStorage(context.Background(), "store01", params, nil)
				}
				if err != nil {
					t.Fatalf("%s: %v", write, err)
				}
				if got := result.GeneratedEncryptionKey.Reveal(); got != tt.want {
					t.Errorf("GeneratedEncryptionKey = %q, want %q", got, tt.want)
				}

				calls := rec.recorded()
				if len(calls) != 1 {
					t.Fatalf("Proxmox received %d requests, want 1", len(calls))
				}
				if got := calls[0].form.Get("encryption-key"); got != tt.sent {
					t.Errorf("Proxmox was sent encryption-key=%q, want %q verbatim", got, tt.sent)
				}
			})
		}
	}
}

// TestSplitStorageDeleteList_ReadsAListTheWayProxmoxDoes transcribes split_list
// (pve-common src/PVE/ParseUtils.pm), which PVE::JSONSchema uses to read the
// update's `delete` (a pve-configid-list): commas, semicolons and whitespace
// separate, empty entries drop out, and NUL separates too. Unicode whitespace
// separates on both sides — pveproxy UTF-8-decodes form values
// (decode_urlencoded, pve-http-server src/PVE/APIServer/AnyEvent.pm), so
// Perl's \s matches U+00A0 there as well. The last two rows are the one place
// this reads further than Proxmox would: a list holding a NUL, which Proxmox
// splits on the NULs alone and then refuses — "bwlimit,nodes" is not one name,
// and neither is the empty entry between two NULs.
func TestSplitStorageDeleteList_ReadsAListTheWayProxmoxDoes(t *testing.T) {
	for _, tt := range []struct {
		list string
		want []string
	}{
		{"", nil},
		{"prune-backups", []string{"prune-backups"}},
		{"prune-backups,bwlimit", []string{"prune-backups", "bwlimit"}},
		{"prune-backups;bwlimit", []string{"prune-backups", "bwlimit"}},
		{"prune-backups bwlimit", []string{"prune-backups", "bwlimit"}},
		{"prune-backups\tbwlimit\nnodes\r\fpreallocation", []string{"prune-backups", "bwlimit", "nodes", "preallocation"}},
		{" ,prune-backups,, ;bwlimit ; ", []string{"prune-backups", "bwlimit"}},
		{"prune-backups\x00bwlimit", []string{"prune-backups", "bwlimit"}},
		{"prune-backups\u00a0bwlimit", []string{"prune-backups", "bwlimit"}},
		{"prune-backups\x00bwlimit,nodes", []string{"prune-backups", "bwlimit", "nodes"}},
		{"prune-backups\x00\x00bwlimit", []string{"prune-backups", "bwlimit"}},
	} {
		got := SplitStorageDeleteList(tt.list)
		if len(got) == 0 && len(tt.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("SplitStorageDeleteList(%q) = %q, want %q", tt.list, got, tt.want)
		}
	}
}

// TestUpdateStorage_SendsTheDeleteListItWasGiven pins the whole form an update
// sends, not just the delete key, so that the list arriving intact and nothing
// else creeping in are both asserted.
//
// Any name is forwarded: which settings may be cleared is Proxmox's to decide,
// and the released handler forwarded its list untouched. What the client adds
// is reading each entry the way Proxmox will and sending those names joined by
// commas, so an entry that is itself a list arrives as the names in it.
func TestUpdateStorage_SendsTheDeleteListItWasGiven(t *testing.T) {
	tests := []struct {
		name    string
		params  url.Values
		options []string
		want    url.Values
	}{
		{
			name:    "remove the encryption key",
			options: []string{"encryption-key"},
			want:    url.Values{"delete": {"encryption-key"}},
		},
		{
			name:    "clear both, joined the way Proxmox splits them",
			params:  url.Values{"content": {"backup"}},
			options: []string{"nodes", "encryption-key"},
			want:    url.Values{"content": {"backup"}, "delete": {"nodes,encryption-key"}},
		},
		{
			name:   "nothing to clear sends no delete at all",
			params: url.Values{"content": {"backup"}},
			want:   url.Values{"content": {"backup"}},
		},
		{
			name:    "a setting Nexara's own dialog never clears",
			options: []string{"prune-backups"},
			want:    url.Values{"delete": {"prune-backups"}},
		},
		{
			name:    "several settings, name for name",
			params:  url.Values{"content": {"backup"}},
			options: []string{"prune-backups", "bwlimit", "preallocation"},
			want:    url.Values{"content": {"backup"}, "delete": {"prune-backups,bwlimit,preallocation"}},
		},
		{
			name:    "entries that are themselves lists arrive as the names in them",
			options: []string{"prune-backups;bwlimit nodes", "max-protected-backups\x00preallocation"},
			want: url.Values{"delete": {
				"prune-backups,bwlimit,nodes,max-protected-backups,preallocation",
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, rec := newStorageWriteServer(t, `{"data":{"storage":"store01","type":"pbs","config":{}}}`)
			sentParams := url.Values{}
			for k, v := range tt.params {
				sentParams[k] = append([]string(nil), v...)
			}

			if _, err := client.UpdateStorage(context.Background(), "store01", sentParams, tt.options); err != nil {
				t.Fatalf("UpdateStorage: %v", err)
			}
			calls := rec.recorded()
			if len(calls) != 1 {
				t.Fatalf("Proxmox received %d requests, want 1", len(calls))
			}
			if calls[0].method != http.MethodPut || calls[0].path != "/api2/json/storage/store01" {
				t.Errorf("request = %s %s, want PUT /api2/json/storage/store01", calls[0].method, calls[0].path)
			}
			if !reflect.DeepEqual(calls[0].form, tt.want) {
				t.Errorf("form = %v, want %v", calls[0].form, tt.want)
			}
			// The delete list goes on a copy: the caller's own params are the
			// settings it asked to write, and a "delete" appearing in them
			// would read as one more setting.
			if len(sentParams) != len(tt.params) || (len(tt.params) > 0 && !reflect.DeepEqual(sentParams, tt.params)) {
				t.Errorf("UpdateStorage changed the caller's params to %v, want %v", sentParams, tt.params)
			}
		})
	}
}

// TestUpdateStorage_RefusesAContradictoryDeleteWithoutIssuingARequest covers
// the two requests UpdateStorage refuses: a delete list that would bypass the
// checked one, and a setting both written and cleared — including one hidden
// in an entry that is itself a list, which is why the check runs on the names
// Proxmox will read rather than on the entries as given.
//
// Each refusal has a TWIN: the same request with only the refused part taken
// away, which must reach Proxmox. Without it a refusal could be passing for an
// unrelated reason — an empty storage name, say — and prove nothing about the
// rule it names.
func TestUpdateStorage_RefusesAContradictoryDeleteWithoutIssuingARequest(t *testing.T) {
	tests := []struct {
		name          string
		params        url.Values
		options       []string
		twinParams    url.Values
		twinOptions   []string
		wantInMessage string
	}{
		{
			// A "delete" among the settings would reach Proxmox as the delete
			// list itself — url.Values has one namespace — skipping every check.
			name:          "a delete smuggled in among the settings",
			params:        url.Values{"delete": {"password"}, "content": {"backup"}},
			twinParams:    url.Values{"content": {"backup"}},
			wantInMessage: `not as a "delete" setting`,
		},
		{
			// Proxmox would NOT refuse this one: encryption-key is sensitive,
			// and extract_sensitive_params lets the new value win over the
			// delete. So the new key would be installed and the delete ignored.
			name:          "a key both generated and removed",
			params:        url.Values{"encryption-key": {PBSEncryptionKeyAutogen}},
			options:       []string{"encryption-key"},
			twinParams:    url.Values{"encryption-key": {PBSEncryptionKeyAutogen}},
			wantInMessage: "encryption-key cannot be set and cleared in the same request",
		},
		{
			name:          "a node restriction both set and lifted",
			params:        url.Values{"nodes": {"pve-01"}},
			options:       []string{"nodes"},
			twinOptions:   []string{"nodes"},
			wantInMessage: "nodes cannot be set and cleared in the same request",
		},
		{
			// Taken as given, the entry names no setting that params writes;
			// Proxmox would read encryption-key out of it all the same.
			name:          "a key both generated and removed from inside a list entry",
			params:        url.Values{"encryption-key": {PBSEncryptionKeyAutogen}},
			options:       []string{"bwlimit;encryption-key"},
			twinOptions:   []string{"bwlimit;encryption-key"},
			wantInMessage: "encryption-key cannot be set and cleared in the same request",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			twin, twinRec := newStorageWriteServer(t, `{"data":{"storage":"store01","type":"pbs","config":{}}}`)
			if _, err := twin.UpdateStorage(context.Background(), "store01", tt.twinParams, tt.twinOptions); err != nil {
				t.Fatalf("precondition: the twin request without the refused part failed: %v", err)
			}
			if n := len(twinRec.recorded()); n != 1 {
				t.Fatalf("precondition: the twin request reached Proxmox %d times, want 1", n)
			}

			client, rec := newStorageWriteServer(t, `{"data":{"storage":"store01","type":"pbs","config":{}}}`)
			_, err := client.UpdateStorage(context.Background(), "store01", tt.params, tt.options)
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("UpdateStorage error = %v, want ErrInvalidInput", err)
			}
			if !strings.Contains(err.Error(), tt.wantInMessage) {
				t.Errorf("error = %q, want it to say %q", err, tt.wantInMessage)
			}
			if calls := rec.recorded(); len(calls) != 0 {
				t.Errorf("Proxmox received %v, want no request at all", calls)
			}
		})
	}
}

// TestPBSEncryptionKey_NeverRendersItsText renders a key — and a
// StorageWriteResult holding one as an exported field, which is how it
// travels — through every route this package can foresee a debugging line
// taking, and fails if the key material appears in any of them.
//
// Both slog handlers are exercised because they reach the key differently:
// the text handler through fmt's %+v, which is Format; the JSON handler,
// which is the one production installs, through encoding/json, which is
// MarshalJSON.
//
// %p and %w are the verbs fmt cannot hand to Format: both take its bad-verb
// path, which skips every method and prints the operand raw. They print no
// placeholder, so for them the test asks only that the key is absent — which
// holds because the raw print of the struct is the address its text sits
// behind.
func TestPBSEncryptionKey_NeverRendersItsText(t *testing.T) {
	key := newPBSEncryptionKey(storageWriteKey(storageWriteCanary))
	result := StorageWriteResult{GeneratedEncryptionKey: key}

	// The positive control: the canary really is in the value, and Reveal is
	// the way to it. Without this every absence below could be an empty key.
	if !strings.Contains(key.Reveal(), storageWriteCanary) {
		t.Fatal("precondition: the fixture key does not carry the canary")
	}

	renderings := map[string]string{}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d"} {
		renderings["fmt "+verb+" key"] = fmt.Sprintf(verb, key)
		renderings["fmt "+verb+" &key"] = fmt.Sprintf(verb, &key)
		renderings["fmt "+verb+" result"] = fmt.Sprintf(verb, result)
		renderings["fmt "+verb+" &result"] = fmt.Sprintf(verb, &result)
	}
	// Held in variables so that vet's printf check, which cannot see through
	// them, does not refuse to build a test that misuses these on purpose.
	unformattable := map[string]string{}
	for _, verb := range []string{"%p", "%w"} {
		unformattable["fmt "+verb+" key"] = fmt.Sprintf(verb, key)
		unformattable["fmt "+verb+" result"] = fmt.Sprintf(verb, result)
		unformattable["fmt.Errorf "+verb+" key"] = fmt.Errorf(verb, key).Error()
	}
	renderings["fmt.Sprint"] = fmt.Sprint(key, result)
	renderings["error wrap"] = fmt.Errorf("update storage store01: %v: %w", result, errors.New("boom")).Error()

	for name, v := range map[string]any{"key": key, "result": result} {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("json.Marshal(%s): %v", name, err)
		}
		renderings["json "+name] = string(raw)
	}

	for name, newHandler := range map[string]func(io.Writer) slog.Handler{
		"text": func(w io.Writer) slog.Handler { return slog.NewTextHandler(w, nil) },
		"json": func(w io.Writer) slog.Handler { return slog.NewJSONHandler(w, nil) },
	} {
		var buf bytes.Buffer
		slog.New(newHandler(&buf)).Info("storage write", "key", key, "result", result, slog.Any("any", result))
		renderings["slog "+name] = buf.String()
	}

	for name, out := range renderings {
		if strings.Contains(out, storageWriteCanary) {
			t.Errorf("%s printed the key material: %s", name, out)
		}
		// Every rendering has to have produced SOMETHING, and something that
		// says what was left out — an empty string would pass the check above
		// without having rendered anything.
		if !strings.Contains(out, redactedPBSEncryptionKey) {
			t.Errorf("%s did not print the placeholder: %q", name, out)
		}
	}
	// A key in an unexported field of some other struct: fmt cannot call a
	// method through such a field, so it prints the field raw. It must still
	// print only the address.
	holder := struct{ k PBSEncryptionKey }{key}
	if out := fmt.Sprintf("%+v", holder); strings.Contains(out, storageWriteCanary) {
		t.Errorf("a key in an unexported field printed the key material: %s", out)
	} else if !strings.Contains(out, "text:") {
		t.Errorf("a key in an unexported field was not printed raw, so the probe proves nothing: %q", out)
	}

	for name, out := range unformattable {
		if strings.Contains(out, storageWriteCanary) {
			t.Errorf("%s printed the key material: %s", name, out)
		}
		// The bad-verb path names the type it refused before printing the
		// operand, so an empty or unrelated output means the probe did not
		// run the path it is here for.
		if !strings.Contains(out, "PBSEncryptionKey") && !strings.Contains(out, "StorageWriteResult") {
			t.Errorf("%s did not take fmt's bad-verb path: %q", name, out)
		}
	}
}
