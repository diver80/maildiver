package native

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

var ErrThunderbirdDisconnected = errors.New("thunderbird extension is not connected")

type rpcResult struct {
	OK     bool
	Result json.RawMessage
	Error  string
}

// RPCBroker lets the Go host (and the local REST bridge) issue reverse-RPC calls
// to the running Thunderbird extension over the active Native Messaging Conn.
type RPCBroker struct {
	mu      sync.Mutex
	conn    *Conn
	pending map[string]chan rpcResult
	seq     atomic.Uint64
}

func NewRPCBroker() *RPCBroker {
	return &RPCBroker{
		pending: make(map[string]chan rpcResult),
	}
}

func (b *RPCBroker) Attach(conn *Conn) {
	b.mu.Lock()
	b.conn = conn
	b.mu.Unlock()
}

func (b *RPCBroker) Detach() {
	b.mu.Lock()
	b.conn = nil
	for id, ch := range b.pending {
		close(ch)
		delete(b.pending, id)
	}
	b.mu.Unlock()
}

func (b *RPCBroker) Connected() bool {
	b.mu.Lock()
	ok := b.conn != nil
	b.mu.Unlock()
	return ok
}

// Call sends a bridge call: the add-on checks it against the bridge policy (bridge.enabled,
// bridge.accountIds, read-only, allowSend).
func (b *RPCBroker) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return b.call(ctx, method, params, "")
}

// CallHost sends a call of the host's own jobs (triage scans, archive runs). The add-on accepts
// only its "host.*" methods this way and checks them against the mailbox policy instead of the
// bridge policy. Bridge clients cannot reach this path: the bridge server only uses Call.
func (b *RPCBroker) CallHost(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return b.call(ctx, method, params, "host")
}

func (b *RPCBroker) call(ctx context.Context, method string, params any, origin string) (json.RawMessage, error) {
	b.mu.Lock()
	conn := b.conn
	if conn == nil {
		b.mu.Unlock()
		return nil, ErrThunderbirdDisconnected
	}
	id := fmt.Sprintf("rpc-%d", b.seq.Add(1))
	ch := make(chan rpcResult, 1)
	b.pending[id] = ch
	b.mu.Unlock()

	cleanup := func() {
		b.mu.Lock()
		delete(b.pending, id)
		b.mu.Unlock()
	}

	payload := map[string]any{
		"method": method,
	}
	if origin != "" {
		payload["origin"] = origin
	}
	if params != nil {
		payload["params"] = params
	}
	if err := conn.Write(Response{
		ID:     id,
		Event:  "tb_rpc",
		OK:     true,
		Result: payload,
	}); err != nil {
		cleanup()
		return nil, err
	}

	select {
	case <-ctx.Done():
		cleanup()
		return nil, ctx.Err()
	case res, ok := <-ch:
		if !ok {
			return nil, ErrThunderbirdDisconnected
		}
		if !res.OK {
			if res.Error == "" {
				return nil, fmt.Errorf("thunderbird rpc %s failed", method)
			}
			return nil, errors.New(res.Error)
		}
		return res.Result, nil
	}
}

func (b *RPCBroker) Resolve(id string, ok bool, result json.RawMessage, errMsg string) {
	if id == "" {
		return
	}
	b.mu.Lock()
	ch, exists := b.pending[id]
	if exists {
		delete(b.pending, id)
	}
	b.mu.Unlock()
	if exists {
		ch <- rpcResult{OK: ok, Result: result, Error: errMsg}
		close(ch)
	}
}

type BridgeEvent struct {
	Event     string          `json:"event"`
	Data      json.RawMessage `json:"data,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
}

// EventHub fans out live Thunderbird, calendar, triage, and theme events to SSE subscribers.
type EventHub struct {
	mu   sync.RWMutex
	subs map[uint64]chan BridgeEvent
	seq  atomic.Uint64
}

func NewEventHub() *EventHub {
	return &EventHub{
		subs: make(map[uint64]chan BridgeEvent),
	}
}

func (h *EventHub) Subscribe() (uint64, <-chan BridgeEvent) {
	id := h.seq.Add(1)
	ch := make(chan BridgeEvent, 64)
	h.mu.Lock()
	h.subs[id] = ch
	h.mu.Unlock()
	return id, ch
}

func (h *EventHub) Unsubscribe(id uint64) {
	h.mu.Lock()
	if ch, ok := h.subs[id]; ok {
		delete(h.subs, id)
		close(ch)
	}
	h.mu.Unlock()
}

func (h *EventHub) Publish(eventName string, data any) {
	var raw json.RawMessage
	if data != nil {
		switch v := data.(type) {
		case json.RawMessage:
			raw = v
		case []byte:
			raw = v
		default:
			if b, err := json.Marshal(v); err == nil {
				raw = b
			}
		}
	}
	ev := BridgeEvent{
		Event:     eventName,
		Data:      raw,
		Timestamp: time.Now().UTC(),
	}
	h.mu.RLock()
	for _, ch := range h.subs {
		select {
		case ch <- ev:
		default:
			// Slow subscriber: drop oldest pressure rather than stalling Thunderbird native messaging.
		}
	}
	h.mu.RUnlock()
}
