package ws

import (
	"encoding/json"
	"log/slog"
	"sync"
)

const clientSendBuffer = 256

// Hub maintains the set of active clients and broadcasts messages to them.
// All mutable state is confined to a single goroutine (CSP pattern).
type Hub struct {
	// Channels for the event loop.
	registerCh   chan *Client
	unregisterCh chan *Client
	subscribeCh  chan subscribeReq
	unsubCh      chan subscribeReq
	broadcastCh  chan broadcastMsg
	stopCh       chan struct{}

	logger *slog.Logger

	// wg tracks the run goroutine for graceful shutdown.
	wg sync.WaitGroup
}

type subscribeReq struct {
	client  *Client
	channel string
}

type broadcastMsg struct {
	room    string
	payload json.RawMessage
}

// NewHub creates a new Hub.
func NewHub(logger *slog.Logger) *Hub {
	return &Hub{
		registerCh:   make(chan *Client, 64),
		unregisterCh: make(chan *Client, 64),
		subscribeCh:  make(chan subscribeReq, 64),
		unsubCh:      make(chan subscribeReq, 64),
		broadcastCh:  make(chan broadcastMsg, 256),
		stopCh:       make(chan struct{}),
		logger:       logger,
	}
}

// Run starts the Hub event loop. Call Stop() to shut it down.
func (h *Hub) Run() {
	h.wg.Add(1)
	go h.run()
}

// Stop signals the Hub to shut down and waits for it to finish.
func (h *Hub) Stop() {
	close(h.stopCh)
	h.wg.Wait()
}

// Register adds a client to the hub.
func (h *Hub) Register(c *Client) {
	h.registerCh <- c
}

// Unregister removes a client from the hub.
func (h *Hub) Unregister(c *Client) {
	h.unregisterCh <- c
}

// Subscribe adds a client to a room.
func (h *Hub) Subscribe(c *Client, channel string) {
	h.subscribeCh <- subscribeReq{client: c, channel: channel}
}

// Unsubscribe removes a client from a room.
func (h *Hub) Unsubscribe(c *Client, channel string) {
	h.unsubCh <- subscribeReq{client: c, channel: channel}
}

// Broadcast sends a message to all clients in a room.
func (h *Hub) Broadcast(room string, payload json.RawMessage) {
	h.broadcastCh <- broadcastMsg{room: room, payload: payload}
}

// hubState holds all mutable state for the hub event loop.
type hubState struct {
	clients     map[*Client]struct{}
	rooms       map[string]map[*Client]struct{}
	clientRooms map[*Client]map[string]struct{}
	lastMsg     map[string][]byte
}

func newHubState() *hubState {
	return &hubState{
		clients:     make(map[*Client]struct{}),
		rooms:       make(map[string]map[*Client]struct{}),
		clientRooms: make(map[*Client]map[string]struct{}),
		lastMsg:     make(map[string][]byte),
	}
}

func (h *Hub) run() {
	defer h.wg.Done()

	st := newHubState()

	for {
		select {
		case <-h.stopCh:
			for c := range st.clients {
				h.removeClient(st, c)
			}
			return

		case c := <-h.registerCh:
			st.clients[c] = struct{}{}
			h.logger.Debug("client registered", "client", c.id)

		case c := <-h.unregisterCh:
			h.removeClient(st, c)

		case req := <-h.subscribeCh:
			c, channel := req.client, req.channel
			// Auto-register if not yet registered (handles channel buffering race).
			if _, ok := st.clients[c]; !ok {
				st.clients[c] = struct{}{}
			}
			// Add to room.
			if st.rooms[channel] == nil {
				st.rooms[channel] = make(map[*Client]struct{})
			}
			st.rooms[channel][c] = struct{}{}

			// Track for client.
			if st.clientRooms[c] == nil {
				st.clientRooms[c] = make(map[string]struct{})
			}
			st.clientRooms[c][channel] = struct{}{}

			// Send confirmation.
			h.trySendOrEvict(st, c, newSubscribedMsg(channel))

			// Send last cached message if available.
			if cached, ok := st.lastMsg[channel]; ok {
				h.trySendOrEvict(st, c, cached)
			}

			h.logger.Debug("client subscribed", "client", c.id, "channel", channel)

		case req := <-h.unsubCh:
			c, channel := req.client, req.channel
			if members, ok := st.rooms[channel]; ok {
				delete(members, c)
				if len(members) == 0 {
					delete(st.rooms, channel)
				}
			}
			if subs, ok := st.clientRooms[c]; ok {
				delete(subs, channel)
			}
			h.logger.Debug("client unsubscribed", "client", c.id, "channel", channel)

		case msg := <-h.broadcastCh:
			data := newDataMsg(msg.room, msg.payload)
			st.lastMsg[msg.room] = data

			members, ok := st.rooms[msg.room]
			if !ok {
				continue
			}
			for c := range members {
				h.trySendOrEvict(st, c, data)
			}
		}
	}
}

// trySendOrEvict performs a non-blocking send to a client. If the client's
// buffer is full, it is evicted as a slow client with full cleanup.
func (h *Hub) trySendOrEvict(st *hubState, c *Client, data []byte) {
	select {
	case c.send <- data:
	default:
		h.logger.Warn("evicting slow client", "client", c.id)
		h.removeClient(st, c)
	}
}

// removeClient removes a client from all hub state and closes its send channel.
func (h *Hub) removeClient(st *hubState, c *Client) {
	if _, ok := st.clients[c]; !ok {
		return
	}
	if subs, ok := st.clientRooms[c]; ok {
		for room := range subs {
			if members, ok := st.rooms[room]; ok {
				delete(members, c)
				if len(members) == 0 {
					delete(st.rooms, room)
				}
			}
		}
		delete(st.clientRooms, c)
	}
	delete(st.clients, c)
	c.closeSend()
	h.logger.Debug("client removed", "client", c.id)
}
