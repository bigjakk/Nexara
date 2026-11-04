package changelog

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A release body is built from commit subjects, and GitHub lists anyone one
// @-mentions as a contributor (v1.17.0 listed "pve" for a subject's "*@pve").
// This runs the real generator over a throwaway repository, so it covers the
// escaping rules and that the generator applies them.
func TestGenerateReleaseNotes_BackticksMentions(t *testing.T) {
	t.Parallel()
	for _, tool := range []string{"bash", "git", "awk"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	script, err := filepath.Abs("../../scripts/generate-release-notes.sh")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	// Git's own variables (a hook's GIT_DIR) would aim these commands at another
	// repository, and a global hooks path would run its hooks here.
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	run := func(name string, args ...string) string {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir, cmd.Env = dir, env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	git := func(args ...string) {
		run("git", append([]string{"-c", "user.name=test", "-c", "user.email=test@example.invalid",
			"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false",
			"-c", "init.defaultBranch=main"}, args...)...)
	}

	cases := []struct{ subject, want string }{
		{"fix: a user created as *@pve explains nothing", "- a user created as *`@pve` explains nothing"},
		{"@alice starts a line", "- `@alice` starts a line"},
		{"fix: (@bob) and @org/team-a.", "- (`@bob`) and `@org/team-a`."},
		{"fix: root@pam, o'brien@ad and @ alone stay", "- root@pam, o'brien@ad and @ alone stay"},
		{"fix: `@types/node` and ``x`@dave`` stay", "- `@types/node` and ``x`@dave`` stay"},
		{"fix: an odd ` then @carol", "- an odd \\` then `@carol`"},
		{"feat: quote the ` in names", "- quote the ` in names"},
		{"fix: see https://www.npmjs.com/package/@tanstack/react-query or www.npmjs.com/package/@types/node, per @erin",
			"- see https://www.npmjs.com/package/@tanstack/react-query or www.npmjs.com/package/@types/node, per `@erin`"},
	}
	git("init", "-q")
	for _, tc := range cases {
		git("commit", "-q", "--allow-empty", "-m", tc.subject)
	}
	git("tag", "v0.0.1")
	notes := run("bash", script, "v0.0.1")

	for _, tc := range cases {
		if !strings.Contains(notes, tc.want+"\n") {
			t.Errorf("subject %q: want the line %q in the notes:\n%s", tc.subject, tc.want, notes)
		}
	}
}
