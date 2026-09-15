package reports

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
)

func TestBuildMIMEMessage_RoundTrips(t *testing.T) {
	t.Parallel()
	raw, err := BuildMIMEMessage("nexara@example.com", []string{"ops@example.com", "boss@example.com"}, Message{
		Subject:  "Nexara report: Backup compliance · cluster01 · 7 days\r\nBcc: attacker@example.com",
		HTMLBody: "<html><body>digest</body></html>",
		Attachments: []Attachment{
			{Filename: `../etc/passwd"; evil=x`, ContentType: "text/html", Data: bytes.Repeat([]byte("<b>report</b>"), 500)},
			{Filename: "backup-compliance-cluster01-2026-09-14.csv", ContentType: "text/csv", Data: []byte("a,b\n1,2\n")},
		},
	})
	if err != nil {
		t.Fatalf("BuildMIMEMessage: %v", err)
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("message does not parse: %v\n%s", err, raw)
	}
	// Header injection is neutralised: the CRLF is stripped, so no Bcc line exists.
	if msg.Header.Get("Bcc") != "" {
		t.Error("CRLF in the subject produced a Bcc header")
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil {
		t.Fatalf("decode subject: %v", err)
	}
	if !strings.HasPrefix(subject, "Nexara report: Backup compliance · cluster01") {
		t.Errorf("subject = %q", subject)
	}
	if msg.Header.Get("To") != "ops@example.com, boss@example.com" {
		t.Errorf("To = %q", msg.Header.Get("To"))
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/mixed" {
		t.Fatalf("content type = %q (%v)", msg.Header.Get("Content-Type"), err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	var parts []struct {
		ct, disp string
		body     []byte
	}
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("next part: %v", err)
		}
		if p.Header.Get("Content-Transfer-Encoding") != "base64" {
			t.Errorf("part %q is not base64", p.Header.Get("Content-Type"))
		}
		decoded, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, p))
		if err != nil {
			t.Fatalf("decode part: %v", err)
		}
		parts = append(parts, struct {
			ct, disp string
			body     []byte
		}{p.Header.Get("Content-Type"), p.Header.Get("Content-Disposition"), decoded})
	}
	if len(parts) != 3 {
		t.Fatalf("got %d parts, want body + 2 attachments", len(parts))
	}
	if string(parts[0].body) != "<html><body>digest</body></html>" || !strings.HasPrefix(parts[0].ct, "text/html") {
		t.Errorf("body part = %q %q", parts[0].ct, parts[0].body)
	}
	// The hostile filename is reduced to safe characters, in both headers.
	if !strings.Contains(parts[1].disp, `filename="etc-passwd-evil-x"`) || !strings.Contains(parts[1].ct, `name="etc-passwd-evil-x"`) {
		t.Errorf("attachment headers = %q / %q", parts[1].ct, parts[1].disp)
	}
	if !bytes.Equal(parts[1].body, bytes.Repeat([]byte("<b>report</b>"), 500)) {
		t.Error("attachment body did not round-trip")
	}
	if string(parts[2].body) != "a,b\n1,2\n" || !strings.Contains(parts[2].disp, "backup-compliance-cluster01-2026-09-14.csv") {
		t.Errorf("csv part = %q %q", parts[2].disp, parts[2].body)
	}
	// No line in the raw message exceeds the SMTP limit.
	for _, line := range bytes.Split(raw, []byte("\r\n")) {
		if len(line) > 998 {
			t.Fatalf("line of %d bytes exceeds the SMTP limit", len(line))
		}
	}
}

func TestAttachmentBaseName(t *testing.T) {
	t.Parallel()
	got := AttachmentBaseName(&ReportData{ReportType: "backup_compliance", ClusterName: "Cluster 01/prod", TimeRange: TimeRange{EndTime: "2026-09-14T06:00:00.123Z"}})
	if got != "backup-compliance-cluster-01-prod-2026-09-14" {
		t.Errorf("AttachmentBaseName = %q", got)
	}
	if safeFilename("///") != "report" {
		t.Errorf("an all-unsafe name falls back to 'report', got %q", safeFilename("///"))
	}
}

func TestReportMessage_AttachesWhatWasAsked(t *testing.T) {
	t.Parallel()
	data := sampleReport()
	m := ReportMessage(data, "<html>full</html>", "a,b\n", false, DigestOptions{RunID: "r1"})
	if len(m.Attachments) != 1 || !strings.HasSuffix(m.Attachments[0].Filename, ".html") {
		t.Errorf("html-only message attachments = %+v", m.Attachments)
	}
	if !strings.HasPrefix(m.Subject, "Nexara report: ") || !strings.Contains(m.HTMLBody, m.Attachments[0].Filename) {
		t.Errorf("subject %q or body does not name the attachment", m.Subject)
	}
	m = ReportMessage(data, "<html>full</html>", "a,b\n", true, DigestOptions{})
	if len(m.Attachments) != 2 || !strings.HasSuffix(m.Attachments[1].Filename, ".csv") {
		t.Errorf("csv message attachments = %+v", m.Attachments)
	}
}
