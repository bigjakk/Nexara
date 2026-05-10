package proxmox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
)

func (c *Client) GetACMEAccounts(ctx context.Context) ([]ACMEAccount, error) {
	var accounts []ACMEAccount
	if err := c.do(ctx, "/cluster/acme/account", &accounts); err != nil {
		return nil, fmt.Errorf("get ACME accounts: %w", err)
	}
	return accounts, nil
}
func (c *Client) CreateACMEAccount(ctx context.Context, params CreateACMEAccountParams) (string, error) {
	form := url.Values{}
	form.Set("contact", params.Contact)
	if params.Name != "" {
		form.Set("name", params.Name)
	}
	if params.Directory != "" {
		form.Set("directory", params.Directory)
	}
	if params.TOSUrl != "" {
		form.Set("tos_url", params.TOSUrl)
	}
	var upid string
	if err := c.doPost(ctx, "/cluster/acme/account", form, &upid); err != nil {
		return "", fmt.Errorf("create ACME account: %w", err)
	}
	return upid, nil
}
func (c *Client) GetACMEAccount(ctx context.Context, name string) (*ACMEAccount, error) {
	path := "/cluster/acme/account/" + url.PathEscape(name)
	var account ACMEAccount
	if err := c.do(ctx, path, &account); err != nil {
		return nil, fmt.Errorf("get ACME account %s: %w", name, err)
	}
	return &account, nil
}
func (c *Client) UpdateACMEAccount(ctx context.Context, name string, params UpdateACMEAccountParams) error {
	form := url.Values{}
	if params.Contact != "" {
		form.Set("contact", params.Contact)
	}
	path := "/cluster/acme/account/" + url.PathEscape(name)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update ACME account %s: %w", name, err)
	}
	return nil
}
func (c *Client) DeleteACMEAccount(ctx context.Context, name string) error {
	path := "/cluster/acme/account/" + url.PathEscape(name)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete ACME account %s: %w", name, err)
	}
	return nil
}
func (c *Client) GetACMEPlugins(ctx context.Context) ([]ACMEPlugin, error) {
	var plugins []ACMEPlugin
	if err := c.do(ctx, "/cluster/acme/plugins", &plugins); err != nil {
		return nil, fmt.Errorf("get ACME plugins: %w", err)
	}
	return plugins, nil
}
func (c *Client) CreateACMEPlugin(ctx context.Context, params CreateACMEPluginParams) error {
	form := url.Values{}
	form.Set("id", params.ID)
	form.Set("type", params.Type)
	if params.API != "" {
		form.Set("api", params.API)
	}
	if params.Data != "" {
		form.Set("data", base64.StdEncoding.EncodeToString([]byte(params.Data)))
	}
	if params.ValidationDelay != nil {
		form.Set("validation-delay", fmt.Sprintf("%d", *params.ValidationDelay))
	}
	if err := c.doPost(ctx, "/cluster/acme/plugins", form, nil); err != nil {
		return fmt.Errorf("create ACME plugin %s: %w", params.ID, err)
	}
	return nil
}
func (c *Client) GetACMEPlugin(ctx context.Context, id string) (*ACMEPlugin, error) {
	path := "/cluster/acme/plugins/" + url.PathEscape(id)
	var plugin ACMEPlugin
	if err := c.do(ctx, path, &plugin); err != nil {
		return nil, fmt.Errorf("get ACME plugin %s: %w", id, err)
	}
	return &plugin, nil
}
func (c *Client) UpdateACMEPlugin(ctx context.Context, id string, params UpdateACMEPluginParams) error {
	form := url.Values{}
	if params.API != "" {
		form.Set("api", params.API)
	}
	if params.Data != "" {
		form.Set("data", base64.StdEncoding.EncodeToString([]byte(params.Data)))
	}
	if params.ValidationDelay != nil {
		form.Set("validation-delay", fmt.Sprintf("%d", *params.ValidationDelay))
	}
	if params.Digest != "" {
		form.Set("digest", params.Digest)
	}
	path := "/cluster/acme/plugins/" + url.PathEscape(id)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update ACME plugin %s: %w", id, err)
	}
	return nil
}
func (c *Client) DeleteACMEPlugin(ctx context.Context, id string) error {
	path := "/cluster/acme/plugins/" + url.PathEscape(id)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete ACME plugin %s: %w", id, err)
	}
	return nil
}
func (c *Client) GetACMEDirectories(ctx context.Context) ([]ACMEDirectory, error) {
	var dirs []ACMEDirectory
	if err := c.do(ctx, "/cluster/acme/directories", &dirs); err != nil {
		return nil, fmt.Errorf("get ACME directories: %w", err)
	}
	return dirs, nil
}
func (c *Client) GetACMETOS(ctx context.Context) (string, error) {
	var tos string
	if err := c.do(ctx, "/cluster/acme/tos", &tos); err != nil {
		return "", fmt.Errorf("get ACME TOS: %w", err)
	}
	return tos, nil
}
func (c *Client) GetACMEChallengeSchema(ctx context.Context) ([]ACMEChallengeSchema, error) {
	var schemas []ACMEChallengeSchema
	if err := c.do(ctx, "/cluster/acme/challenge-schema", &schemas); err != nil {
		return nil, fmt.Errorf("get ACME challenge schema: %w", err)
	}
	return schemas, nil
}
func (c *Client) GetACMEChallengeSchemaRaw(ctx context.Context, dst *json.RawMessage) error {
	return c.do(ctx, "/cluster/acme/challenge-schema", dst)
}
