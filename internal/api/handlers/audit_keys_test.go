package handlers

import "testing"

// aclPathKey mirrors PVE::AccessControl::normalize_path, whose edge is the root:
// "/" and "//" are both "/", and only a trailing slash after a segment goes.
func TestACLPathKey(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"/vms/100":   "/vms/100",
		"/vms/100/":  "/vms/100",
		"/vms//100":  "/vms/100",
		"//vms///x/": "/vms/x",
		"/":          "/",
		"//":         "/",
	} {
		if got := aclPathKey(in); got != want {
			t.Errorf("aclPathKey(%q) = %q, want %q", in, got, want)
		}
	}
}
