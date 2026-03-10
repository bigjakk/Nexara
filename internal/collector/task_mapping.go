package collector

type taskMapping struct {
	ResourceType string
	Action       string
}

var proxmoxTaskMap = map[string]taskMapping{
	// QEMU VM actions
	"qmstart":    {ResourceType: "vm", Action: "start"},
	"qmstop":     {ResourceType: "vm", Action: "stop"},
	"qmshutdown": {ResourceType: "vm", Action: "shutdown"},
	"qmreboot":   {ResourceType: "vm", Action: "reboot"},
	"qmreset":    {ResourceType: "vm", Action: "reset"},
	"qmsuspend":  {ResourceType: "vm", Action: "suspend"},
	"qmresume":   {ResourceType: "vm", Action: "resume"},
	"qmclone":    {ResourceType: "vm", Action: "clone"},
	"qmdestroy":  {ResourceType: "vm", Action: "destroy"},
	"qmcreate":   {ResourceType: "vm", Action: "create"},
	"qmmigrate":  {ResourceType: "vm", Action: "migrate"},
	"qmigrate":   {ResourceType: "vm", Action: "migrate"},
	"qmmove":     {ResourceType: "vm", Action: "move_disk"},
	"qmconfig":   {ResourceType: "vm", Action: "config_change"},
	"qmtemplate": {ResourceType: "vm", Action: "convert_template"},
	"qmsnapshot": {ResourceType: "vm", Action: "snapshot"},
	"qmrollback": {ResourceType: "vm", Action: "rollback"},

	// LXC container actions
	"vzcreate":   {ResourceType: "container", Action: "create"},
	"vzstart":    {ResourceType: "container", Action: "start"},
	"vzstop":     {ResourceType: "container", Action: "stop"},
	"vzshutdown": {ResourceType: "container", Action: "shutdown"},
	"vzreboot":   {ResourceType: "container", Action: "reboot"},
	"vzdestroy":  {ResourceType: "container", Action: "destroy"},
	"vzmigrate":  {ResourceType: "container", Action: "migrate"},
	"vzclone":    {ResourceType: "container", Action: "clone"},

	// Backup
	"vzdump": {ResourceType: "backup", Action: "backup"},

	// Node-level actions
	"startall":  {ResourceType: "node", Action: "start_all"},
	"stopall":   {ResourceType: "node", Action: "stop_all"},
	"aptupdate": {ResourceType: "node", Action: "apt_update"},

	// Storage
	"download": {ResourceType: "storage", Action: "download"},
	"imgcopy":  {ResourceType: "storage", Action: "image_copy"},

	// Network / service management
	"srvreload":       {ResourceType: "node", Action: "service_reload"},
	"reloadnetworkall": {ResourceType: "network", Action: "reload"},

	// ACME / certificates
	"acmenewcert":  {ResourceType: "node", Action: "acme_new_cert"},
	"acmeregister": {ResourceType: "node", Action: "acme_register"},
}

// skipTaskTypes contains Proxmox task types that should be silently ignored
// (too noisy or not actionable for audit purposes).
var skipTaskTypes = map[string]bool{
	"vncproxy":   true,
	"vncshell":   true,
	"qmmonitor":  true,
	"spiceproxy": true,
	"login":      true,
}
