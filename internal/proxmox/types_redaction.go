package proxmox

import (
	"fmt"
	"log/slog"
	"strings"
)

// The renderings below keep the credential each of these types carries out of
// logs and error text, as AccessUser's do: String, GoString and LogValue cover
// fmt's %v/%s/%+v/%#v and slog, and print every other field. There is
// deliberately no MarshalJSON: the client sends the params types to Proxmox as
// forms built from their fields, and the handlers return some of the others as
// JSON, with the secret withheld where it must be (sdnIPAMsForRead, for one).
// Value receivers, so a value held in a struct or slice is covered too. Each
// method renders a copy, with the secret fields replaced, through a local type
// that has no methods, so the rendering cannot call itself.

// redactedRendering is name's rendering of plain, a method-free copy: with
// goSyntax as %#v gives it, under the type's own name, otherwise as %+v does.
func redactedRendering(name string, plain any, goSyntax bool) string {
	if !goSyntax {
		return name + fmt.Sprintf("%+v", plain)
	}
	s := fmt.Sprintf("%#v", plain)
	return "proxmox." + name + s[strings.IndexByte(s, '{'):]
}

// TermProxyResponse: the console ticket and the VNC password: either opens the guest's console.
func (t TermProxyResponse) String() string {
	t.Ticket, t.Password = redactedIfSet(t.Ticket), redactedIfSet(t.Password)
	type plain TermProxyResponse
	return redactedRendering("TermProxyResponse", plain(t), false)
}

func (t TermProxyResponse) GoString() string {
	t.Ticket, t.Password = redactedIfSet(t.Ticket), redactedIfSet(t.Password)
	type plain TermProxyResponse
	return redactedRendering("TermProxyResponse", plain(t), true)
}

func (t TermProxyResponse) LogValue() slog.Value { return slog.StringValue(t.String()) }

// StorageConfig: a CIFS or PBS password, a PBS encryption key and a Ceph keyring.
func (s StorageConfig) String() string {
	s.Password, s.EncryptionKey, s.Keyring = redactedIfSet(s.Password), redactedIfSet(s.EncryptionKey), redactedIfSet(s.Keyring)
	type plain StorageConfig
	return redactedRendering("StorageConfig", plain(s), false)
}

func (s StorageConfig) GoString() string {
	s.Password, s.EncryptionKey, s.Keyring = redactedIfSet(s.Password), redactedIfSet(s.EncryptionKey), redactedIfSet(s.Keyring)
	type plain StorageConfig
	return redactedRendering("StorageConfig", plain(s), true)
}

func (s StorageConfig) LogValue() slog.Value { return slog.StringValue(s.String()) }

// CreateCTParams: the container's root password.
func (c CreateCTParams) String() string {
	c.Password = redactedIfSet(c.Password)
	type plain CreateCTParams
	return redactedRendering("CreateCTParams", plain(c), false)
}

func (c CreateCTParams) GoString() string {
	c.Password = redactedIfSet(c.Password)
	type plain CreateCTParams
	return redactedRendering("CreateCTParams", plain(c), true)
}

func (c CreateCTParams) LogValue() slog.Value { return slog.StringValue(c.String()) }

// SDNIPAM: the IPAM's API token.
func (s SDNIPAM) String() string {
	s.Token = redactedIfSet(s.Token)
	type plain SDNIPAM
	return redactedRendering("SDNIPAM", plain(s), false)
}

func (s SDNIPAM) GoString() string {
	s.Token = redactedIfSet(s.Token)
	type plain SDNIPAM
	return redactedRendering("SDNIPAM", plain(s), true)
}

func (s SDNIPAM) LogValue() slog.Value { return slog.StringValue(s.String()) }

// CreateSDNIPAMParams: the IPAM's API token.
func (c CreateSDNIPAMParams) String() string {
	c.Token = redactedIfSet(c.Token)
	type plain CreateSDNIPAMParams
	return redactedRendering("CreateSDNIPAMParams", plain(c), false)
}

func (c CreateSDNIPAMParams) GoString() string {
	c.Token = redactedIfSet(c.Token)
	type plain CreateSDNIPAMParams
	return redactedRendering("CreateSDNIPAMParams", plain(c), true)
}

func (c CreateSDNIPAMParams) LogValue() slog.Value { return slog.StringValue(c.String()) }

// UpdateSDNIPAMParams: the IPAM's API token.
func (u UpdateSDNIPAMParams) String() string {
	u.Token = redactedIfSet(u.Token)
	type plain UpdateSDNIPAMParams
	return redactedRendering("UpdateSDNIPAMParams", plain(u), false)
}

func (u UpdateSDNIPAMParams) GoString() string {
	u.Token = redactedIfSet(u.Token)
	type plain UpdateSDNIPAMParams
	return redactedRendering("UpdateSDNIPAMParams", plain(u), true)
}

func (u UpdateSDNIPAMParams) LogValue() slog.Value { return slog.StringValue(u.String()) }

// SDNDNS: the DNS plugin's API key.
func (s SDNDNS) String() string {
	s.Key = redactedIfSet(s.Key)
	type plain SDNDNS
	return redactedRendering("SDNDNS", plain(s), false)
}

func (s SDNDNS) GoString() string {
	s.Key = redactedIfSet(s.Key)
	type plain SDNDNS
	return redactedRendering("SDNDNS", plain(s), true)
}

func (s SDNDNS) LogValue() slog.Value { return slog.StringValue(s.String()) }

// CreateSDNDNSParams: the DNS plugin's API key.
func (c CreateSDNDNSParams) String() string {
	c.Key = redactedIfSet(c.Key)
	type plain CreateSDNDNSParams
	return redactedRendering("CreateSDNDNSParams", plain(c), false)
}

func (c CreateSDNDNSParams) GoString() string {
	c.Key = redactedIfSet(c.Key)
	type plain CreateSDNDNSParams
	return redactedRendering("CreateSDNDNSParams", plain(c), true)
}

func (c CreateSDNDNSParams) LogValue() slog.Value { return slog.StringValue(c.String()) }

// UpdateSDNDNSParams: the DNS plugin's API key.
func (u UpdateSDNDNSParams) String() string {
	u.Key = redactedIfSet(u.Key)
	type plain UpdateSDNDNSParams
	return redactedRendering("UpdateSDNDNSParams", plain(u), false)
}

func (u UpdateSDNDNSParams) GoString() string {
	u.Key = redactedIfSet(u.Key)
	type plain UpdateSDNDNSParams
	return redactedRendering("UpdateSDNDNSParams", plain(u), true)
}

func (u UpdateSDNDNSParams) LogValue() slog.Value { return slog.StringValue(u.String()) }

// MetricServerConfig: the metric server's API token.
func (m MetricServerConfig) String() string {
	m.Token = redactedIfSet(m.Token)
	type plain MetricServerConfig
	return redactedRendering("MetricServerConfig", plain(m), false)
}

func (m MetricServerConfig) GoString() string {
	m.Token = redactedIfSet(m.Token)
	type plain MetricServerConfig
	return redactedRendering("MetricServerConfig", plain(m), true)
}

func (m MetricServerConfig) LogValue() slog.Value { return slog.StringValue(m.String()) }

// CreateMetricServerParams: the metric server's API token.
func (c CreateMetricServerParams) String() string {
	c.Token = redactedIfSet(c.Token)
	type plain CreateMetricServerParams
	return redactedRendering("CreateMetricServerParams", plain(c), false)
}

func (c CreateMetricServerParams) GoString() string {
	c.Token = redactedIfSet(c.Token)
	type plain CreateMetricServerParams
	return redactedRendering("CreateMetricServerParams", plain(c), true)
}

func (c CreateMetricServerParams) LogValue() slog.Value { return slog.StringValue(c.String()) }

// UpdateMetricServerParams: the metric server's API token.
func (u UpdateMetricServerParams) String() string {
	u.Token = redactedIfSet(u.Token)
	type plain UpdateMetricServerParams
	return redactedRendering("UpdateMetricServerParams", plain(u), false)
}

func (u UpdateMetricServerParams) GoString() string {
	u.Token = redactedIfSet(u.Token)
	type plain UpdateMetricServerParams
	return redactedRendering("UpdateMetricServerParams", plain(u), true)
}

func (u UpdateMetricServerParams) LogValue() slog.Value { return slog.StringValue(u.String()) }
