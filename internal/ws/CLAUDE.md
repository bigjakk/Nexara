# internal/ws — WebSocket Conventions

## Architecture
- **Hub** manages all active connections and subscriptions
- **Redis pub/sub** fans out messages across multiple ws service instances
- Clients authenticate via JWT on initial WebSocket handshake

## Subscription Model
- Clients subscribe to named channels (e.g., `node:123:metrics`)
- Hub routes published messages only to subscribed clients
- Subscribe/unsubscribe via JSON control frames

## Frame Types
- **Text frames**: JSON messages for structured data (metrics, events, alerts)
- **Binary frames**: raw data for console proxy (xterm.js, noVNC)

## Adding a New Channel
1. Define the channel name pattern in `channels.go`
2. Add the publisher that pushes to Redis pub/sub
3. Hub picks up messages and fans out to subscribed clients

## Rules
- Always validate JWT before upgrading to WebSocket
- Use ping/pong for connection health checks
- Close stale connections after timeout
