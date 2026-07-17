package events

import (
	"testing"
	"time"
)

func TestPublishSubscribe(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe("dep-1")
	defer cancel()
	h.Publish("dep-1", []byte("line one"))
	h.Publish("dep-2", []byte("other topic"))
	select {
	case msg := <-ch:
		if string(msg) != "line one" {
			t.Fatalf("msg = %s", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("no message received")
	}
	select {
	case msg := <-ch:
		t.Fatalf("unexpected cross-topic message: %s", msg)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestCancelUnsubscribes(t *testing.T) {
	h := NewHub()
	_, cancel := h.Subscribe("dep-1")
	cancel()
	h.Publish("dep-1", []byte("x")) // must not panic or block
}
