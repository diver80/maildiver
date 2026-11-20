// Package events fans live engine events (new mail, changed mails, calendar changes, scan and
// archive progress) out to subscribers inside the process – the app's views and the tray.
package events

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"
)

// Event is one published event; Data is its JSON payload.
type Event struct {
	Event     string          `json:"event"`
	Data      json.RawMessage `json:"data,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
}

// Hub delivers every published event to all current subscribers.
type Hub struct {
	mu   sync.RWMutex
	subs map[uint64]chan Event
	seq  atomic.Uint64
}

func NewHub() *Hub {
	return &Hub{subs: make(map[uint64]chan Event)}
}

// Subscribe returns a subscription ID and its channel (buffered; a slow subscriber misses events
// rather than blocking the engine).
func (h *Hub) Subscribe() (uint64, <-chan Event) {
	id := h.seq.Add(1)
	ch := make(chan Event, 64)
	h.mu.Lock()
	h.subs[id] = ch
	h.mu.Unlock()
	return id, ch
}

// Unsubscribe closes the subscription's channel.
func (h *Hub) Unsubscribe(id uint64) {
	h.mu.Lock()
	if ch, ok := h.subs[id]; ok {
		delete(h.subs, id)
		close(ch)
	}
	h.mu.Unlock()
}

// Publish sends an event; data is marshalled to JSON unless it already is JSON.
func (h *Hub) Publish(name string, data any) {
	if h == nil {
		return
	}
	var raw json.RawMessage
	switch v := data.(type) {
	case nil:
	case json.RawMessage:
		raw = v
	case []byte:
		raw = v
	default:
		if b, err := json.Marshal(v); err == nil {
			raw = b
		}
	}
	ev := Event{Event: name, Data: raw, Timestamp: time.Now().UTC()}
	h.mu.RLock()
	for _, ch := range h.subs {
		select {
		case ch <- ev:
		default:
		}
	}
	h.mu.RUnlock()
}
