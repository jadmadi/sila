package daemon

import (
	"fmt"
	"testing"
	"time"
)

func TestBusDeliversInOrder(t *testing.T) {
	bus := NewBus()
	defer bus.Close()
	sub := bus.Subscribe()
	received := make(chan Event, 1000)
	go func() {
		for event := range sub.Events() {
			received <- event
		}
	}()
	for i := 0; i < 1000; i++ {
		bus.Publish("write", i)
		if i%50 == 0 {
			time.Sleep(time.Millisecond)
		}
	}
	for i := 0; i < 1000; i++ {
		select {
		case event := <-received:
			if event.Seq != uint64(i+1) || event.Payload != i {
				t.Fatalf("event %d = %+v", i, event)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out at %d", i)
		}
	}
}

func TestBusDropsForStalledReader(t *testing.T) {
	bus := NewBus()
	defer bus.Close()
	sub := bus.Subscribe()
	for i := 0; i < subscriberBuffer+10; i++ {
		bus.Publish("write", i)
	}
	// Drain until the dropped counter appears.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-sub.Events():
			if event.Dropped > 0 {
				return
			}
		case <-deadline:
			t.Fatal("no dropped count observed")
		}
	}
}

func TestBusKindFilter(t *testing.T) {
	bus := NewBus()
	defer bus.Close()
	sub := bus.Subscribe("goal")
	bus.Publish("write", 1)
	bus.Publish("goal", 2)
	select {
	case event := <-sub.Events():
		if event.Kind != "goal" || event.Payload != 2 {
			t.Fatalf("event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("no event")
	}
}

func TestBusCloseAndDoubleCancel(t *testing.T) {
	bus := NewBus()
	sub := bus.Subscribe()
	sub.Close()
	sub.Close()
	bus.Publish("write", 1)
	bus.Close()
	bus.Publish("write", 2)
	if _, ok := <-sub.Events(); ok {
		t.Fatal("channel not closed")
	}
}

func TestBusNoSubscribersAndAfterClose(t *testing.T) {
	bus := NewBus()
	bus.Publish("write", 1)
	bus.Close()
	bus.Publish("write", 2)
	sub := bus.Subscribe()
	if sub == nil {
		t.Fatal("subscribe after close returned nil")
	}
	select {
	case _, ok := <-sub.Events():
		if ok {
			t.Fatal("subscribe after close should yield a closed channel")
		}
	case <-time.After(time.Second):
		t.Fatal("subscribe after close hung")
	}
	_ = fmt.Sprint
}
