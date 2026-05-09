package proxmox

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

func (c *Client) GetClusterFirewallRules(ctx context.Context) ([]FirewallRule, error) {
	var rules []FirewallRule
	if err := c.do(ctx, "/cluster/firewall/rules", &rules); err != nil {
		return nil, fmt.Errorf("get cluster firewall rules: %w", err)
	}
	return rules, nil
}
func (c *Client) CreateClusterFirewallRule(ctx context.Context, rule FirewallRuleParams) error {
	form := firewallRuleToForm(rule)
	if err := c.doPost(ctx, "/cluster/firewall/rules", form, nil); err != nil {
		return fmt.Errorf("create cluster firewall rule: %w", err)
	}
	return nil
}
func (c *Client) UpdateClusterFirewallRule(ctx context.Context, pos int, rule FirewallRuleParams) error {
	form := firewallRuleToForm(rule)
	path := "/cluster/firewall/rules/" + strconv.Itoa(pos)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update cluster firewall rule %d: %w", pos, err)
	}
	return nil
}
func (c *Client) DeleteClusterFirewallRule(ctx context.Context, pos int) error {
	path := "/cluster/firewall/rules/" + strconv.Itoa(pos)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete cluster firewall rule %d: %w", pos, err)
	}
	return nil
}
func (c *Client) GetNodeFirewallRules(ctx context.Context, node string) ([]FirewallRule, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/firewall/rules"
	var rules []FirewallRule
	if err := c.do(ctx, path, &rules); err != nil {
		return nil, fmt.Errorf("get firewall rules on %s: %w", node, err)
	}
	return rules, nil
}
func (c *Client) UpdateNodeFirewallRule(ctx context.Context, node string, pos int, rule FirewallRuleParams) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	form := firewallRuleToForm(rule)
	path := "/nodes/" + url.PathEscape(node) + "/firewall/rules/" + strconv.Itoa(pos)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update firewall rule %d on %s: %w", pos, node, err)
	}
	return nil
}
func (c *Client) GetVMFirewallRules(ctx context.Context, node string, vmid int) ([]FirewallRule, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/firewall/rules"
	var rules []FirewallRule
	if err := c.do(ctx, path, &rules); err != nil {
		return nil, fmt.Errorf("get firewall rules for VM %d on %s: %w", vmid, node, err)
	}
	return rules, nil
}
func (c *Client) CreateVMFirewallRule(ctx context.Context, node string, vmid int, rule FirewallRuleParams) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if err := validateVMID(vmid); err != nil {
		return err
	}
	form := firewallRuleToForm(rule)
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/firewall/rules"
	if err := c.doPost(ctx, path, form, nil); err != nil {
		return fmt.Errorf("create firewall rule for VM %d on %s: %w", vmid, node, err)
	}
	return nil
}
func (c *Client) UpdateVMFirewallRule(ctx context.Context, node string, vmid int, pos int, rule FirewallRuleParams) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if err := validateVMID(vmid); err != nil {
		return err
	}
	form := firewallRuleToForm(rule)
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/firewall/rules/" + strconv.Itoa(pos)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update firewall rule %d for VM %d on %s: %w", pos, vmid, node, err)
	}
	return nil
}
func (c *Client) DeleteVMFirewallRule(ctx context.Context, node string, vmid int, pos int) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if err := validateVMID(vmid); err != nil {
		return err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/firewall/rules/" + strconv.Itoa(pos)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete firewall rule %d for VM %d on %s: %w", pos, vmid, node, err)
	}
	return nil
}
func (c *Client) GetClusterFirewallOptions(ctx context.Context) (*FirewallOptions, error) {
	var opts FirewallOptions
	if err := c.do(ctx, "/cluster/firewall/options", &opts); err != nil {
		return nil, fmt.Errorf("get cluster firewall options: %w", err)
	}
	return &opts, nil
}
func (c *Client) SetClusterFirewallOptions(ctx context.Context, opts FirewallOptions) error {
	form := firewallOptionsToForm(opts)
	if err := c.doPut(ctx, "/cluster/firewall/options", form, nil); err != nil {
		return fmt.Errorf("set cluster firewall options: %w", err)
	}
	return nil
}
func (c *Client) GetFirewallAliases(ctx context.Context) ([]FirewallAlias, error) {
	var aliases []FirewallAlias
	if err := c.do(ctx, "/cluster/firewall/aliases", &aliases); err != nil {
		return nil, fmt.Errorf("get firewall aliases: %w", err)
	}
	return aliases, nil
}
func (c *Client) CreateFirewallAlias(ctx context.Context, params FirewallAliasParams) error {
	form := url.Values{}
	form.Set("name", params.Name)
	form.Set("cidr", params.CIDR)
	if params.Comment != "" {
		form.Set("comment", params.Comment)
	}
	if err := c.doPost(ctx, "/cluster/firewall/aliases", form, nil); err != nil {
		return fmt.Errorf("create firewall alias %s: %w", params.Name, err)
	}
	return nil
}
func (c *Client) GetFirewallAlias(ctx context.Context, name string) (*FirewallAlias, error) {
	path := "/cluster/firewall/aliases/" + url.PathEscape(name)
	var alias FirewallAlias
	if err := c.do(ctx, path, &alias); err != nil {
		return nil, fmt.Errorf("get firewall alias %s: %w", name, err)
	}
	return &alias, nil
}
func (c *Client) UpdateFirewallAlias(ctx context.Context, name string, params FirewallAliasParams) error {
	form := url.Values{}
	form.Set("cidr", params.CIDR)
	if params.Comment != "" {
		form.Set("comment", params.Comment)
	}
	if params.Rename != "" {
		form.Set("rename", params.Rename)
	}
	path := "/cluster/firewall/aliases/" + url.PathEscape(name)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update firewall alias %s: %w", name, err)
	}
	return nil
}
func (c *Client) DeleteFirewallAlias(ctx context.Context, name string) error {
	path := "/cluster/firewall/aliases/" + url.PathEscape(name)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete firewall alias %s: %w", name, err)
	}
	return nil
}
func (c *Client) GetFirewallIPSets(ctx context.Context) ([]FirewallIPSet, error) {
	var sets []FirewallIPSet
	if err := c.do(ctx, "/cluster/firewall/ipset", &sets); err != nil {
		return nil, fmt.Errorf("get firewall IP sets: %w", err)
	}
	return sets, nil
}
func (c *Client) CreateFirewallIPSet(ctx context.Context, name, comment string) error {
	form := url.Values{}
	form.Set("name", name)
	if comment != "" {
		form.Set("comment", comment)
	}
	if err := c.doPost(ctx, "/cluster/firewall/ipset", form, nil); err != nil {
		return fmt.Errorf("create firewall IP set %s: %w", name, err)
	}
	return nil
}
func (c *Client) DeleteFirewallIPSet(ctx context.Context, name string) error {
	path := "/cluster/firewall/ipset/" + url.PathEscape(name)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete firewall IP set %s: %w", name, err)
	}
	return nil
}
func (c *Client) GetFirewallIPSetEntries(ctx context.Context, name string) ([]FirewallIPSetEntry, error) {
	path := "/cluster/firewall/ipset/" + url.PathEscape(name)
	var entries []FirewallIPSetEntry
	if err := c.do(ctx, path, &entries); err != nil {
		return nil, fmt.Errorf("get firewall IP set %s entries: %w", name, err)
	}
	return entries, nil
}
func (c *Client) AddFirewallIPSetEntry(ctx context.Context, setName string, params FirewallIPSetEntryParams) error {
	form := url.Values{}
	form.Set("cidr", params.CIDR)
	if params.NoMatch != nil {
		form.Set("nomatch", strconv.Itoa(*params.NoMatch))
	}
	if params.Comment != "" {
		form.Set("comment", params.Comment)
	}
	path := "/cluster/firewall/ipset/" + url.PathEscape(setName)
	if err := c.doPost(ctx, path, form, nil); err != nil {
		return fmt.Errorf("add entry to IP set %s: %w", setName, err)
	}
	return nil
}
func (c *Client) UpdateFirewallIPSetEntry(ctx context.Context, setName, cidr string, params FirewallIPSetEntryParams) error {
	form := url.Values{}
	if params.NoMatch != nil {
		form.Set("nomatch", strconv.Itoa(*params.NoMatch))
	}
	if params.Comment != "" {
		form.Set("comment", params.Comment)
	}
	path := "/cluster/firewall/ipset/" + url.PathEscape(setName) + "/" + url.PathEscape(cidr)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update entry %s in IP set %s: %w", cidr, setName, err)
	}
	return nil
}
func (c *Client) DeleteFirewallIPSetEntry(ctx context.Context, setName, cidr string) error {
	path := "/cluster/firewall/ipset/" + url.PathEscape(setName) + "/" + url.PathEscape(cidr)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete entry %s from IP set %s: %w", cidr, setName, err)
	}
	return nil
}
func (c *Client) GetFirewallSecurityGroups(ctx context.Context) ([]FirewallSecurityGroup, error) {
	var groups []FirewallSecurityGroup
	if err := c.do(ctx, "/cluster/firewall/groups", &groups); err != nil {
		return nil, fmt.Errorf("get firewall security groups: %w", err)
	}
	return groups, nil
}
func (c *Client) CreateFirewallSecurityGroup(ctx context.Context, params FirewallSecurityGroupParams) error {
	form := url.Values{}
	form.Set("group", params.Group)
	if params.Comment != "" {
		form.Set("comment", params.Comment)
	}
	if err := c.doPost(ctx, "/cluster/firewall/groups", form, nil); err != nil {
		return fmt.Errorf("create security group %s: %w", params.Group, err)
	}
	return nil
}
func (c *Client) DeleteFirewallSecurityGroup(ctx context.Context, group string) error {
	path := "/cluster/firewall/groups/" + url.PathEscape(group)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete security group %s: %w", group, err)
	}
	return nil
}
func (c *Client) GetSecurityGroupRules(ctx context.Context, group string) ([]FirewallRule, error) {
	path := "/cluster/firewall/groups/" + url.PathEscape(group)
	var rules []FirewallRule
	if err := c.do(ctx, path, &rules); err != nil {
		return nil, fmt.Errorf("get security group %s rules: %w", group, err)
	}
	return rules, nil
}
func (c *Client) CreateSecurityGroupRule(ctx context.Context, group string, params FirewallRuleParams) error {
	form := firewallRuleToForm(params)
	path := "/cluster/firewall/groups/" + url.PathEscape(group)
	if err := c.doPost(ctx, path, form, nil); err != nil {
		return fmt.Errorf("create rule in security group %s: %w", group, err)
	}
	return nil
}
func (c *Client) UpdateSecurityGroupRule(ctx context.Context, group string, pos int, params FirewallRuleParams) error {
	form := firewallRuleToForm(params)
	path := "/cluster/firewall/groups/" + url.PathEscape(group) + "/" + strconv.Itoa(pos)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update rule %d in security group %s: %w", pos, group, err)
	}
	return nil
}
func (c *Client) DeleteSecurityGroupRule(ctx context.Context, group string, pos int) error {
	path := "/cluster/firewall/groups/" + url.PathEscape(group) + "/" + strconv.Itoa(pos)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete rule %d from security group %s: %w", pos, group, err)
	}
	return nil
}
