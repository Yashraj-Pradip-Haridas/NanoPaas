package websocket

import (
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

const (
	// Time allowed to write a message to the peer
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer
	pongWait = 60 * time.Second

	// Send pings to peer with this period (must be less than pongWait)
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer
	maxMessageSize = 512

	// Buffer size for client message channel
	messageBufferSize = 256
)

// Client represents a WebSocket client connection
type Client struct {
	ID       uuid.UUID
	Hub      *Hub
	Conn     *websocket.Conn
	Send     chan []byte
	Topics   map[string]bool
	topicsMu sync.RWMutex
}

// Hub maintains the set of active clients and broadcasts messages
type Hub struct {
	// Registered clients
	clients map[*Client]bool

	// Topic subscriptions: topic -> clients
	topics map[string]map[*Client]bool

	// Inbound messages from clients
	broadcast chan *Message

	// Register requests from clients
	register chan *Client

	// Unregister requests from clients
	unregister chan *Client

	// Subscribe to topic
	subscribe chan *Subscription

	// Unsubscribe from topic
	unsubscribe chan *Subscription

	// Mutex for thread-safe operations
	mu sync.RWMutex

	// done channel for graceful shutdown
	done chan struct{}

	logger *zap.Logger
}

// Message represents a message to broadcast
type Message struct {
	Topic   string `json:"topic"`
	Type    string `json:"type"`
	Payload []byte `json:"payload"`
}

// Subscription represents a topic subscription request
type Subscription struct {
	Client *Client
	Topic  string
}

// NewHub creates a new Hub instance
func NewHub(logger *zap.Logger) *Hub {
	return &Hub{
		clients:     make(map[*Client]bool),
		topics:      make(map[string]map[*Client]bool),
		broadcast:   make(chan *Message, 256),
		register:    make(chan *Client),
		unregister:  make(chan *Client),
		subscribe:   make(chan *Subscription),
		unsubscribe: make(chan *Subscription),
		done:        make(chan struct{}),
		logger:      logger,
	}
}

// Run starts the hub's main loop
func (h *Hub) Run() {
	for {
		select {
		case <-h.done:
			// Close all client connections
			h.mu.Lock()
			for client := range h.clients {
				close(client.Send)
				client.Conn.Close()
			}
			h.mu.Unlock()
			return
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
			h.logger.Debug("Client registered", zap.String("client_id", client.ID.String()))

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				// Remove from all topics
				for topic := range client.Topics {
					if clients, exists := h.topics[topic]; exists {
						delete(clients, client)
						if len(clients) == 0 {
							delete(h.topics, topic)
						}
					}
				}
				delete(h.clients, client)
				close(client.Send)
			}
			h.mu.Unlock()
			h.logger.Debug("Client unregistered", zap.String("client_id", client.ID.String()))

		case sub := <-h.subscribe:
			h.mu.Lock()
			if _, exists := h.topics[sub.Topic]; !exists {
				h.topics[sub.Topic] = make(map[*Client]bool)
			}
			h.topics[sub.Topic][sub.Client] = true
			sub.Client.topicsMu.Lock()
			sub.Client.Topics[sub.Topic] = true
			sub.Client.topicsMu.Unlock()
			h.mu.Unlock()
			h.logger.Debug("Client subscribed to topic",
				zap.String("client_id", sub.Client.ID.String()),
				zap.String("topic", sub.Topic),
			)

		case sub := <-h.unsubscribe:
			h.mu.Lock()
			if clients, exists := h.topics[sub.Topic]; exists {
				delete(clients, sub.Client)
				if len(clients) == 0 {
					delete(h.topics, sub.Topic)
				}
			}
			sub.Client.topicsMu.Lock()
			delete(sub.Client.Topics, sub.Topic)
			sub.Client.topicsMu.Unlock()
			h.mu.Unlock()

		case message := <-h.broadcast:
			h.mu.RLock()
			clients := h.topics[message.Topic]
			h.mu.RUnlock()

			for client := range clients {
				select {
				case client.Send <- message.Payload:
				default:
					// Client's send buffer is full, remove them
					h.unregister <- client
				}
			}
		}
	}
}

// Stop gracefully stops the hub
func (h *Hub) Stop() {
	close(h.done)
}

// Broadcast sends a message to all clients subscribed to a topic
func (h *Hub) Broadcast(topic string, messageType string, payload []byte) {
	h.broadcast <- &Message{
		Topic:   topic,
		Type:    messageType,
		Payload: payload,
	}
}

// BroadcastString sends a string message to all clients subscribed to a topic
func (h *Hub) BroadcastString(topic, messageType, payload string) {
	h.Broadcast(topic, messageType, []byte(payload))
}

// Subscribe subscribes a client to a topic
func (h *Hub) Subscribe(client *Client, topic string) {
	h.subscribe <- &Subscription{Client: client, Topic: topic}
}

// Unsubscribe unsubscribes a client from a topic
func (h *Hub) Unsubscribe(client *Client, topic string) {
	h.unsubscribe <- &Subscription{Client: client, Topic: topic}
}

// Register registers a new client
func (h *Hub) Register(client *Client) {
	h.register <- client
}

// Unregister unregisters a client
func (h *Hub) Unregister(client *Client) {
	h.unregister <- client
}

// ClientCount returns the number of connected clients
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// TopicClientCount returns the number of clients subscribed to a topic
func (h *Hub) TopicClientCount(topic string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if clients, exists := h.topics[topic]; exists {
		return len(clients)
	}
	return 0
}

// NewClient creates a new WebSocket client
func NewClient(hub *Hub, conn *websocket.Conn) *Client {
	return &Client{
		ID:     uuid.New(),
		Hub:    hub,
		Conn:   conn,
		Send:   make(chan []byte, messageBufferSize),
		Topics: make(map[string]bool),
	}
}

// ReadPump pumps messages from the WebSocket connection to the hub
func (c *Client) ReadPump() {
	defer func() {
		c.Hub.Unregister(c)
		c.Conn.Close()
	}()

	c.Conn.SetReadLimit(maxMessageSize)
	c.Conn.SetReadDeadline(time.Now().Add(pongWait))
	c.Conn.SetPongHandler(func(string) error {
		c.Conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := c.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				c.Hub.logger.Warn("WebSocket error", zap.Error(err))
			}
			break
		}

		// Handle subscription messages
		// Expected format: {"action": "subscribe", "topic": "build:xxx"}
		_ = message // Process subscription requests here if needed
	}
}

// WritePump pumps messages from the hub to the WebSocket connection
func (c *Client) WritePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.Send:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Hub closed the channel
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.Conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// Add queued messages to the current websocket message
			n := len(c.Send)
			for i := 0; i < n; i++ {
				w.Write([]byte{'\n'})
				w.Write(<-c.Send)
			}

			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
