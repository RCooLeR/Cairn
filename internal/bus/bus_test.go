package bus

import (
	"context"
	"testing"
	"time"
)

func TestPublishSubscribeOrdering(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	b := New()
	defer b.Close()

	ch := b.Subscribe(ctx, TopicObjectsChanged, 4)
	b.Publish(Event{Topic: TopicObjectsChanged, Payload: "one"})
	b.Publish(Event{Topic: TopicObjectsChanged, Payload: "two"})

	if got := receivePayload(t, ch); got != "one" {
		t.Fatalf("first payload = %v, want one", got)
	}
	if got := receivePayload(t, ch); got != "two" {
		t.Fatalf("second payload = %v, want two", got)
	}
}

func TestPublishDropsOldestForSlowSubscriber(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	b := New()
	defer b.Close()

	ch := b.Subscribe(ctx, TopicStatsSample, 2)
	b.Publish(Event{Topic: TopicStatsSample, Payload: 1})
	b.Publish(Event{Topic: TopicStatsSample, Payload: 2})
	b.Publish(Event{Topic: TopicStatsSample, Payload: 3})

	if got := receivePayload(t, ch); got != 2 {
		t.Fatalf("first retained payload = %v, want 2", got)
	}
	if got := receivePayload(t, ch); got != 3 {
		t.Fatalf("second retained payload = %v, want 3", got)
	}
}

func TestSubscribeUnsubscribesOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	b := New()
	defer b.Close()

	ch := b.Subscribe(ctx, TopicProviderStatus, 1)
	cancel()

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatalf("subscription channel still open after cancel")
		}
	case <-time.After(time.Second):
		t.Fatalf("subscription did not close after cancel")
	}

	b.Publish(Event{Topic: TopicProviderStatus, Payload: "late"})
	select {
	case event, ok := <-ch:
		if ok {
			t.Fatalf("received after cancel: %#v", event)
		}
	default:
	}
}

func TestCoalesceLatestEmitsLastEventInWindow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	in := make(chan Event, 3)
	out := CoalesceLatest(ctx, in, 20*time.Millisecond)

	in <- Event{Topic: TopicStatsSample, Payload: "first"}
	in <- Event{Topic: TopicStatsSample, Payload: "second"}
	in <- Event{Topic: TopicStatsSample, Payload: "third"}

	select {
	case event := <-out:
		if event.Payload != "third" {
			t.Fatalf("coalesced payload = %v, want third", event.Payload)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for coalesced event")
	}
}

func TestBatchFlushesOnMaxAndWindow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	in := make(chan Event, 3)
	out := Batch(ctx, in, 50*time.Millisecond, 2)

	in <- Event{Topic: TopicLogsLines, Payload: 1}
	in <- Event{Topic: TopicLogsLines, Payload: 2}

	first := receiveBatch(t, out)
	if len(first) != 2 {
		t.Fatalf("first batch len = %d, want 2", len(first))
	}

	in <- Event{Topic: TopicLogsLines, Payload: 3}
	second := receiveBatch(t, out)
	if len(second) != 1 || second[0].Payload != 3 {
		t.Fatalf("second batch = %#v, want payload 3", second)
	}
}

func receivePayload(t *testing.T, ch <-chan Event) any {
	t.Helper()

	select {
	case event := <-ch:
		return event.Payload
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for event")
	}
	return nil
}

func receiveBatch(t *testing.T, ch <-chan []Event) []Event {
	t.Helper()

	select {
	case batch := <-ch:
		return batch
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for batch")
	}
	return nil
}
