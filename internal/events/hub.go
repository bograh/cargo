// Package events is an in-memory pub/sub hub for streaming deployment
// logs and status to SSE clients.
package events

import "sync"

type Hub struct {
	mu   sync.RWMutex
	subs map[string]map[chan []byte]struct{}
}

func NewHub() *Hub {
	return &Hub{subs: map[string]map[chan []byte]struct{}{}}
}

func (h *Hub) Subscribe(topic string) (<-chan []byte, func()) {
	ch := make(chan []byte, 256)
	h.mu.Lock()
	if h.subs[topic] == nil {
		h.subs[topic] = map[chan []byte]struct{}{}
	}
	h.subs[topic][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs[topic], ch)
		if len(h.subs[topic]) == 0 {
			delete(h.subs, topic)
		}
		h.mu.Unlock()
	}
}

// Publish never blocks; a full subscriber buffer drops the message.
func (h *Hub) Publish(topic string, data []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs[topic] {
		select {
		case ch <- data:
		default:
		}
	}
}
