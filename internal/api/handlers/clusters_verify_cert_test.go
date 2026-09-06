package handlers

import "testing"

// decideCertificateVerify is the whole security decision behind one-click
// re-pinning, so it is worth exercising as a table rather than through the
// handler. The failure that matters is not refusing too often — it is
// accepting a certificate the existing trust chain does not vouch for.
func TestDecideCertificateVerify(t *testing.T) {
	const (
		oldFP = "AA:BB:CC"
		newFP = "DD:EE:FF"
		evil  = "99:88:77"
	)

	tests := []struct {
		name                   string
		pinned, live, attested string
		want                   certVerifyOutcome
	}{
		{
			// The case this exists for: Proxmox rotated the certificate, the
			// collector already saw the new one over a still-trusted channel,
			// and a live handshake agrees.
			name:   "rotated certificate corroborated by both sources",
			pinned: oldFP, live: newFP, attested: newFP,
			want: certVerifyAccept,
		},
		{
			// The signature of an interception: something is answering the
			// address with a certificate the cluster does not report.
			name:   "live handshake disagrees with what the cluster reports",
			pinned: oldFP, live: evil, attested: newFP,
			want: certVerifyRefuseMismatch,
		},
		{
			// No second source, so the live handshake would be trusting
			// whatever answers — exactly the unpinned behaviour this avoids.
			name:   "nothing observed for this endpoint",
			pinned: oldFP, live: newFP, attested: "",
			want: certVerifyRefuseUnattested,
		},
		{
			// Refuse before comparing, so an unattested endpoint can never be
			// accepted by matching a value against itself.
			name:   "unattested is refused even when live equals the pin",
			pinned: oldFP, live: oldFP, attested: "",
			want: certVerifyRefuseUnattested,
		},
		{
			name:   "no live certificate to judge",
			pinned: oldFP, live: "", attested: newFP,
			want: certVerifyRefuseUnattested,
		},
		{
			// Catches a reordering: if Unchanged were tested before Mismatch,
			// this would report success. The endpoint is replaying the
			// certificate we still have pinned, while the cluster reports it
			// has already rotated away from it.
			name:   "endpoint replays the pinned cert the cluster has rotated away from",
			pinned: oldFP, live: oldFP, attested: newFP,
			want: certVerifyRefuseMismatch,
		},
		{
			name:   "already pinned to this certificate",
			pinned: newFP, live: newFP, attested: newFP,
			want: certVerifyUnchanged,
		},
		{
			// Proxmox writes "AA:BB:CC", the client stores "aabbcc"; a
			// spelling difference must not read as a rotation to repair.
			name:   "spelling differences are the same certificate",
			pinned: "aabbcc", live: oldFP, attested: oldFP,
			want: certVerifyUnchanged,
		},
		{
			// The vacuous-guard trap: EndpointCertificateChanged reports
			// "unchanged" whenever a side is empty, so without the explicit
			// pinned != "" this would answer Unchanged and never pin.
			name:   "an unpinned cluster gets pinned, not reported as unchanged",
			pinned: "", live: newFP, attested: newFP,
			want: certVerifyAccept,
		},
		{
			name:   "an unpinned cluster is still refused when the sources disagree",
			pinned: "", live: evil, attested: newFP,
			want: certVerifyRefuseMismatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decideCertificateVerify(tt.pinned, tt.live, tt.attested); got != tt.want {
				t.Errorf("decideCertificateVerify(%q, %q, %q) = %v, want %v",
					tt.pinned, tt.live, tt.attested, got, tt.want)
			}
		})
	}
}
