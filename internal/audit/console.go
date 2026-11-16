package audit

import "encoding/json"

// ConsoleOpenRefused is the action of the one row a refused console open
// writes, whether the mint refused the permission (internal/api/handlers,
// ConsoleToken) or the WebSocket gate refused a token for another console
// (internal/ws). Its resource is the user ("auth", the user id), as the mint's.
const ConsoleOpenRefused = "console_open_refused"

// Why a console open was refused.
const (
	ConsoleRefusedPermission = "permission" // no console:<resource> on the cluster
	ConsoleRefusedScope      = "scope"      // the token was minted for another console
)

// ConsoleOpenRefusedDetails is that row's details: the reason and the console
// type, a value checked against its vocabulary before it gets here. Nothing a
// caller typed goes in (no node name, no query value): view:audit is held by
// every built-in Viewer.
func ConsoleOpenRefusedDetails(reason, consoleType string) json.RawMessage {
	details, _ := json.Marshal(map[string]string{"reason": reason, "type": consoleType})
	return details
}
