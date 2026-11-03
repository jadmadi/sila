package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Event is one SSE notification pushed to connected clients.
type Event struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Data      any    `json:"data,omitempty"`
}

// EventBroker manages Server-Sent Events subscriptions and fan-out.
type EventBroker struct {
	mu         sync.RWMutex
	clients    map[chan Event]struct{}
	register   chan chan Event
	unregister chan chan Event
	broadcast  chan Event
}

// NewEventBroker constructs an initialized event broker.
func NewEventBroker() *EventBroker {
	return &EventBroker{
		clients:    make(map[chan Event]struct{}),
		register:   make(chan chan Event),
		unregister: make(chan chan Event),
		broadcast:  make(chan Event, 32),
	}
}

// Start launches the background dispatch loop for client subscriptions and broadcasts.
func (b *EventBroker) Start(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			b.mu.Lock()
			for ch := range b.clients {
				close(ch)
			}
			b.clients = make(map[chan Event]struct{})
			b.mu.Unlock()
			return

		case ch := <-b.register:
			b.mu.Lock()
			b.clients[ch] = struct{}{}
			b.mu.Unlock()

		case ch := <-b.unregister:
			b.mu.Lock()
			if _, ok := b.clients[ch]; ok {
				delete(b.clients, ch)
				close(ch)
			}
			b.mu.Unlock()

		case ev := <-b.broadcast:
			b.mu.RLock()
			for ch := range b.clients {
				select {
				case ch <- ev:
				default:
					// Slow consumer drop
				}
			}
			b.mu.RUnlock()

		case <-ticker.C:
			// Heartbeat keepalive
			b.Broadcast("heartbeat", map[string]string{
				"time": time.Now().UTC().Format(time.RFC3339),
			})
		}
	}
}

// Broadcast dispatches an event to all active SSE subscribers.
func (b *EventBroker) Broadcast(eventType string, data any) {
	ev := Event{
		Type:      eventType,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Data:      data,
	}
	select {
	case b.broadcast <- ev:
	default:
	}
}

// ServeHTTP handles the SSE streaming endpoint.
func (b *EventBroker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// No Access-Control-Allow-Origin: the cockpit UI is same-origin, and the
	// server middleware rejects cross-origin connections outright.

	clientChan := make(chan Event, 16)
	b.register <- clientChan
	defer func() {
		b.unregister <- clientChan
	}()

	// Send initial connection event
	initData, _ := json.Marshal(Event{
		Type:      "connected",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Data:      map[string]string{"status": "ok"},
	})
	fmt.Fprintf(w, "event: connected\ndata: %s\n\n", initData)
	flusher.Flush()

	notify := r.Context().Done()
	for {
		select {
		case <-notify:
			return
		case ev, ok := <-clientChan:
			if !ok {
				return
			}
			dataBytes, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, dataBytes)
			flusher.Flush()
		}
	}
}
