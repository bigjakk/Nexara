package redact

import "testing"

// TestURLs holds where each part of a URL ends: userinfo at the last "@" before
// the authority does (so a password holding "@" or a space goes whole, an "@" in
// a path is none), the query and fragment at what ends the URL in a text.
func TestURLs(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ in, want string }{
		{"https://user01:p@ss@host.example.com:8443/a", "https://(set)@host.example.com:8443/a"},
		{"https://user01:pa ss@host.example.com", "https://(set)@host.example.com"},
		{"https://TOKEN@host.example.com/a", "https://(set)@host.example.com/a"},
		{"https://host.example.com/a@b?c=d@e", "https://host.example.com/a@b?(set)"},
		{"from ftp://a:b@one.example.com to s3://c@two.example.com", "from ftp://(set)@one.example.com to s3://(set)@two.example.com"},
		{"user01@example.com", "user01@example.com"},
		{"https://bucket.example.com/f.iso?X-Amz-Signature=abc&X-Amz-Credential=def", "https://bucket.example.com/f.iso?(set)"},
		{"downloading https://h.example.com/f?sig=abc to /var/lib/vz/f", "downloading https://h.example.com/f?(set) to /var/lib/vz/f"},
		{"download of 'https://h.example.com/f?sig=abc' finished", "download of 'https://h.example.com/f?(set)' finished"},
		{"https://h.example.com/p?a=1#access_token=abc", "https://h.example.com/p?(set)#(set)"},
		{"https://h.example.com/p#key", "https://h.example.com/p#(set)"},
		{"https://u:p@h.example.com/p?next=https://i.example.com/?tok=1", "https://(set)@h.example.com/p?(set)"},
		{"https://h.example.com/p? and https://h.example.com/q#", "https://h.example.com/p? and https://h.example.com/q#"},
		{"what? #1 of 2", "what? #1 of 2"},
	} {
		got := URLs(tt.in)
		if got != tt.want {
			t.Errorf("URLs(%q) = %q, want %q", tt.in, got, tt.want)
		}
		if again := URLs(got); again != got {
			t.Errorf("URLs(%q) a second time = %q, want it unchanged", got, again)
		}
	}
}

// TestTargetURL holds what is left of a webhook's URL: its scheme and host.
func TestTargetURL(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ in, want string }{
		{"https://hooks.example.com/services/T000/B000/XXXX", "https://hooks.example.com/(set)"},
		{"https://api.example.com/bot123:ABC/sendMessage", "https://api.example.com/(set)"},
		{"https://user01:***@hooks.example.com:8443?token=abc", "https://(set)@hooks.example.com:8443/(set)"},
		{"https://hooks.example.com/", "https://hooks.example.com/"},
		{"https://hooks.example.com", "https://hooks.example.com"},
		{"hooks.example.com/services/T000", "(set)"},
	} {
		if got := TargetURL(tt.in); got != tt.want {
			t.Errorf("TargetURL(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
