package reports

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/textproto"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/notifications"
)

type smtpConfig struct {
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Username string   `json:"username"`
	Password string   `json:"password"`
	From     string   `json:"from"`
	To       []string `json:"to"`
	TLS      *bool    `json:"tls,omitempty"`
}

func (c smtpConfig) useSTARTTLS() bool {
	if c.TLS == nil {
		return true
	}
	return *c.TLS
}

// Attachment is one file carried with the digest.
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// Message is what a report email carries: the digest as the body and the
// report itself as attachments.
type Message struct {
	Subject     string
	HTMLBody    string
	Attachments []Attachment
}

// SendReportEmail sends a report through an existing SMTP notification
// channel. recipients overrides the channel's own To list when non-empty.
func SendReportEmail(ctx context.Context, queries *db.Queries, encryptionKey string, channelID uuid.UUID, recipients []string, msg Message, logger *slog.Logger) error {
	channel, err := queries.GetNotificationChannel(ctx, channelID)
	if err != nil {
		return fmt.Errorf("get email channel: %w", err)
	}
	if channel.ChannelType != "email" {
		return fmt.Errorf("channel %s is type %s, not email", channelID, channel.ChannelType)
	}

	configJSON, err := crypto.Decrypt(channel.ConfigEncrypted, encryptionKey)
	if err != nil {
		return fmt.Errorf("decrypt channel config: %w", err)
	}

	var cfg smtpConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return fmt.Errorf("parse smtp config: %w", err)
	}
	if cfg.Host == "" {
		return fmt.Errorf("smtp host not configured")
	}
	if cfg.Port == 0 {
		cfg.Port = 587
	}

	toAddrs := recipients
	if len(toAddrs) == 0 {
		toAddrs = cfg.To
	}
	if len(toAddrs) == 0 {
		return fmt.Errorf("no email recipients specified")
	}

	raw, err := BuildMIMEMessage(cfg.From, toAddrs, msg)
	if err != nil {
		return fmt.Errorf("build message: %w", err)
	}

	if err := notifications.SendSMTPMessage(ctx, notifications.SMTPSendOptions{
		Host:        cfg.Host,
		Port:        cfg.Port,
		Username:    cfg.Username,
		Password:    cfg.Password,
		From:        cfg.From,
		To:          toAddrs,
		Message:     raw,
		UseSTARTTLS: cfg.useSTARTTLS(),
	}); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}

	logger.Info("report email sent", "channel_id", channelID, "recipients", len(toAddrs), "attachments", len(msg.Attachments))
	return nil
}

// BuildMIMEMessage assembles the RFC 5322 message: a multipart/mixed body
// whose first part is the HTML digest and whose remaining parts are the
// attachments, each base64-encoded so no line exceeds what SMTP allows.
// Headers are sanitised against CRLF injection and the subject is RFC 2047
// encoded, since report titles carry non-ASCII punctuation.
func BuildMIMEMessage(from string, to []string, msg Message) ([]byte, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	fmt.Fprintf(&buf, "From: %s\r\n", sanitizeHeader(from))
	fmt.Fprintf(&buf, "To: %s\r\n", sanitizeHeader(strings.Join(to, ", ")))
	fmt.Fprintf(&buf, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", sanitizeHeader(msg.Subject)))
	buf.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&buf, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", mw.Boundary())

	body, err := mw.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {`text/html; charset="UTF-8"`},
		"Content-Transfer-Encoding": {"base64"},
	})
	if err != nil {
		return nil, fmt.Errorf("create body part: %w", err)
	}
	writeBase64(body, []byte(msg.HTMLBody))

	for _, a := range msg.Attachments {
		name := safeFilename(a.Filename)
		ct := a.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		part, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {fmt.Sprintf("%s; name=%q", ct, name)},
			"Content-Disposition":       {fmt.Sprintf("attachment; filename=%q", name)},
			"Content-Transfer-Encoding": {"base64"},
		})
		if err != nil {
			return nil, fmt.Errorf("create attachment part: %w", err)
		}
		writeBase64(part, a.Data)
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("close multipart: %w", err)
	}
	return buf.Bytes(), nil
}

// writeBase64 encodes data in 76-column lines, the canonical MIME width.
func writeBase64(w interface{ Write([]byte) (int, error) }, data []byte) {
	encoded := base64.StdEncoding.EncodeToString(data)
	for len(encoded) > 76 {
		_, _ = w.Write([]byte(encoded[:76] + "\r\n"))
		encoded = encoded[76:]
	}
	_, _ = w.Write([]byte(encoded + "\r\n"))
}

var unsafeFilenameChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// safeFilename keeps an attachment name to ASCII letters, digits, dot, dash
// and underscore, so it cannot smuggle header syntax or path separators.
func safeFilename(name string) string {
	name = unsafeFilenameChars.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-.")
	if name == "" {
		return "report"
	}
	return name
}

// AttachmentBaseName builds the file stem the report attachments share:
// report type, cluster and the period's end date, lower-cased and reduced to
// filename-safe characters.
func AttachmentBaseName(data *ReportData) string {
	end := data.TimeRange.EndTime
	if len(end) >= 10 {
		end = end[:10]
	}
	stem := strings.ToLower(fmt.Sprintf("%s-%s-%s", strings.ReplaceAll(data.ReportType, "_", "-"), data.ClusterName, end))
	return safeFilename(stem)
}

func sanitizeHeader(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return s
}

// ReportMessage assembles the email for a finished run: the digest as the
// body and the report attached as HTML, plus the CSV when asked for.
func ReportMessage(data *ReportData, htmlOutput, csvOutput string, withCSV bool, opts DigestOptions) Message {
	base := AttachmentBaseName(data)
	attachments := []Attachment{{Filename: base + ".html", ContentType: "text/html", Data: []byte(htmlOutput)}}
	if withCSV {
		attachments = append(attachments, Attachment{Filename: base + ".csv", ContentType: "text/csv", Data: []byte(csvOutput)})
	}
	for _, a := range attachments {
		opts.AttachmentNames = append(opts.AttachmentNames, a.Filename)
	}
	return Message{
		Subject:     "Nexara report: " + data.Title,
		HTMLBody:    RenderEmailDigest(data, opts),
		Attachments: attachments,
	}
}
