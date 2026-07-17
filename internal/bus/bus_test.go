package bus

import (
	"context"
	"errors"
	"sync"
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
	// Exercise the mailbox directly so worker scheduling cannot turn an entry
	// into an irrevocable in-flight claim before saturation is established.
	sub := newSubscription(context.Background(), 2)
	sub.publishLossy(Event{Topic: TopicStatsSample, Payload: 1})
	sub.publishLossy(Event{Topic: TopicStatsSample, Payload: 2})
	sub.publishLossy(Event{Topic: TopicStatsSample, Payload: 3})

	_, first := sub.next()
	if got := first.event.Payload; got != 2 {
		t.Fatalf("first retained payload = %v, want 2", got)
	}
	sub.removeLossy(first)
	_, second := sub.next()
	if got := second.event.Payload; got != 3 {
		t.Fatalf("second retained payload = %v, want 3", got)
	}
}

func TestLossyClaimCannotBeInvalidatedBeforeSend(t *testing.T) {
	b := New()
	defer b.Close()

	ch := b.Subscribe(context.Background(), TopicStatsSample, 1)
	sub := onlySubscription(t, b, TopicStatsSample)
	claimed := make(chan *lossyEntry, 2)
	releaseClaim := make(chan struct{})
	setLossyClaimHook(sub, func(entry *lossyEntry) {
		claimed <- entry
		<-releaseClaim
	})

	b.Publish(Event{Topic: TopicStatsSample, Payload: "claimed"})
	first := receiveLossyEntry(t, claimed)
	if got := first.event.Payload; got != "claimed" {
		t.Fatalf("claimed payload = %v, want claimed", got)
	}

	b.Publish(Event{Topic: TopicStatsSample, Payload: "invalidated"})
	sub.mailboxMu.Lock()
	queued := len(sub.lossy)
	if queued != 1 {
		sub.mailboxMu.Unlock()
		t.Fatalf("queued lossy entries = %d, want 1", queued)
	}
	invalidated := sub.lossy[0]
	sub.mailboxMu.Unlock()

	// The worker is paused after next claimed the first entry but before its
	// send select. Saturating now must invalidate only the still-queued entry.
	b.Publish(Event{Topic: TopicStatsSample, Payload: "latest"})
	select {
	case <-invalidated.invalidated:
	case <-time.After(time.Second):
		t.Fatal("queued entry was not invalidated on saturation")
	}
	sub.mailboxMu.Lock()
	invalidatedState := invalidated.state
	claimedState := first.state
	sub.mailboxMu.Unlock()
	if invalidatedState != lossyInvalidated {
		t.Fatalf("evicted entry state = %v, want lossyInvalidated", invalidatedState)
	}
	if claimedState != lossyClaimed {
		t.Fatalf("in-flight entry state = %v, want lossyClaimed", claimedState)
	}

	close(releaseClaim)
	if got := receivePayload(t, ch); got != "claimed" {
		t.Fatalf("first payload = %v, want claimed", got)
	}
	if got := receivePayload(t, ch); got != "latest" {
		t.Fatalf("second payload = %v, want latest", got)
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

func TestSubscribeAfterCloseReturnsClosedChannel(t *testing.T) {
	b := New()
	b.Close()

	ch := b.Subscribe(context.Background(), TopicObjectsChanged, 1)
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("Subscribe after Close returned an open channel")
		}
	case <-time.After(time.Second):
		t.Fatal("Subscribe after Close did not return a closed channel")
	}
}

func TestCloseJoinsBackgroundSubscriptionCleanup(t *testing.T) {
	b := New()
	_ = b.Subscribe(context.Background(), TopicObjectsChanged, 1)

	b.Close()

	done := make(chan struct{})
	go func() {
		b.cleanup.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close returned before subscription cleanup completed")
	}
}

func TestConcurrentCloseWaitsForCleanup(t *testing.T) {
	b := New()
	_ = b.Subscribe(context.Background(), TopicObjectsChanged, 1)

	closed := make(chan struct{}, 2)
	for range 2 {
		go func() {
			b.Close()
			closed <- struct{}{}
		}()
	}
	for range 2 {
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatal("concurrent Close did not complete")
		}
	}
}

func TestPublishRaceWithCloseDoesNotPanic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	b := New()
	_ = b.Subscribe(ctx, TopicObjectsChanged, 1)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 100 {
			b.Publish(Event{Topic: TopicObjectsChanged, Payload: "event"})
		}
	}()

	b.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish loop did not finish after Close")
	}
}

func TestPublishCriticalWaitsForCapacityAndPreservesOrder(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	b := New()
	defer b.Close()

	ch := b.Subscribe(ctx, TopicJobDone, 1)
	sub := onlySubscription(t, b, TopicJobDone)
	if err := b.PublishCritical(context.Background(), Event{Topic: TopicJobDone, Payload: "one"}); err != nil {
		t.Fatalf("publish first critical event: %v", err)
	}
	assertCriticalSlotsExhausted(t, sub)

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- b.PublishCritical(context.Background(), Event{Topic: TopicJobDone, Payload: "two"})
	}()
	waitForCriticalWaiters(t, sub, 1)

	if got := receivePayload(t, ch); got != "one" {
		t.Fatalf("first critical payload = %v, want one", got)
	}
	if err := receiveError(t, secondDone); err != nil {
		t.Fatalf("publish second critical event: %v", err)
	}
	if got := receivePayload(t, ch); got != "two" {
		t.Fatalf("second critical payload = %v, want two", got)
	}
}

func TestPublishCriticalCallerCancellationUnblocksCapacityWait(t *testing.T) {
	subCtx, cancelSub := context.WithCancel(context.Background())
	defer cancelSub()

	b := New()
	defer b.Close()
	_ = b.Subscribe(subCtx, TopicJobDone, 1)
	sub := onlySubscription(t, b, TopicJobDone)

	if err := b.PublishCritical(context.Background(), Event{Topic: TopicJobDone, Payload: "fills mailbox"}); err != nil {
		t.Fatalf("fill critical mailbox: %v", err)
	}

	publishCtx, cancelPublish := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- b.PublishCritical(publishCtx, Event{Topic: TopicJobDone, Payload: "blocked"})
	}()
	waitForCriticalWaiters(t, sub, 1)
	cancelPublish()

	if err := receiveError(t, result); !errors.Is(err, context.Canceled) {
		t.Fatalf("PublishCritical error = %v, want context.Canceled", err)
	}
}

func TestPublishCriticalSubscriptionCancellationUnblocksCapacityWait(t *testing.T) {
	subCtx, cancelSub := context.WithCancel(context.Background())

	b := New()
	defer b.Close()
	ch := b.Subscribe(subCtx, TopicJobDone, 1)
	sub := onlySubscription(t, b, TopicJobDone)

	if err := b.PublishCritical(context.Background(), Event{Topic: TopicJobDone, Payload: "fills mailbox"}); err != nil {
		t.Fatalf("fill critical mailbox: %v", err)
	}

	result := make(chan error, 1)
	go func() {
		result <- b.PublishCritical(context.Background(), Event{Topic: TopicJobDone, Payload: "blocked"})
	}()
	waitForCriticalWaiters(t, sub, 1)
	cancelSub()

	if err := receiveError(t, result); !errors.Is(err, ErrSubscriptionClosed) {
		t.Fatalf("PublishCritical error = %v, want ErrSubscriptionClosed", err)
	}
	assertChannelClosed(t, ch)
}

func TestPublishCriticalCloseUnblocksCapacityWait(t *testing.T) {
	b := New()
	_ = b.Subscribe(context.Background(), TopicJobDone, 1)
	sub := onlySubscription(t, b, TopicJobDone)

	if err := b.PublishCritical(context.Background(), Event{Topic: TopicJobDone, Payload: "fills mailbox"}); err != nil {
		t.Fatalf("fill critical mailbox: %v", err)
	}

	result := make(chan error, 1)
	go func() {
		result <- b.PublishCritical(context.Background(), Event{Topic: TopicJobDone, Payload: "blocked"})
	}()
	waitForCriticalWaiters(t, sub, 1)
	b.Close()

	if err := receiveError(t, result); !errors.Is(err, ErrBusClosed) {
		t.Fatalf("PublishCritical error = %v, want ErrBusClosed", err)
	}
}

func TestPublishCriticalRechecksCallerCancellationAfterCapacityReservation(t *testing.T) {
	b := New()
	defer b.Close()

	ch := b.Subscribe(context.Background(), TopicJobDone, 1)
	sub := onlySubscription(t, b, TopicJobDone)
	if err := b.PublishCritical(context.Background(), Event{Topic: TopicJobDone, Payload: "fills mailbox"}); err != nil {
		t.Fatalf("fill critical mailbox: %v", err)
	}

	reserved, release := installCriticalReserveHook(sub)
	publishCtx, cancelPublish := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- b.PublishCritical(publishCtx, Event{Topic: TopicJobDone, Payload: "must not admit"})
	}()
	waitForCriticalWaiters(t, sub, 1)
	if got := receivePayload(t, ch); got != "fills mailbox" {
		t.Fatalf("first payload = %v, want fills mailbox", got)
	}
	waitForSignal(t, reserved, "publisher to reserve released capacity")

	// Cancellation happens while the publisher owns capacity but before it can
	// perform any post-reservation eligibility check.
	cancelPublish()
	close(release)
	if err := receiveError(t, result); !errors.Is(err, context.Canceled) {
		t.Fatalf("PublishCritical error = %v, want context.Canceled", err)
	}
	assertNoCriticalEntries(t, sub)
	assertCriticalSlotsAvailable(t, sub, 1)
}

func TestPublishCriticalRechecksSubscriptionCancellationAfterCapacityReservation(t *testing.T) {
	subCtx, cancelSub := context.WithCancel(context.Background())
	b := New()
	defer b.Close()

	ch := b.Subscribe(subCtx, TopicJobDone, 1)
	sub := onlySubscription(t, b, TopicJobDone)
	if err := b.PublishCritical(context.Background(), Event{Topic: TopicJobDone, Payload: "fills mailbox"}); err != nil {
		t.Fatalf("fill critical mailbox: %v", err)
	}

	reserved, release := installCriticalReserveHook(sub)
	result := make(chan error, 1)
	go func() {
		result <- b.PublishCritical(context.Background(), Event{Topic: TopicJobDone, Payload: "must not admit"})
	}()
	waitForCriticalWaiters(t, sub, 1)
	if got := receivePayload(t, ch); got != "fills mailbox" {
		t.Fatalf("first payload = %v, want fills mailbox", got)
	}
	waitForSignal(t, reserved, "publisher to reserve released capacity")

	cancelSub()
	close(release)
	if err := receiveError(t, result); !errors.Is(err, ErrSubscriptionClosed) {
		t.Fatalf("PublishCritical error = %v, want ErrSubscriptionClosed", err)
	}
	assertChannelClosed(t, ch)
}

func TestPublishCriticalRechecksCloseAfterCapacityReservation(t *testing.T) {
	b := New()
	defer b.Close()

	ch := b.Subscribe(context.Background(), TopicJobDone, 1)
	sub := onlySubscription(t, b, TopicJobDone)
	if err := b.PublishCritical(context.Background(), Event{Topic: TopicJobDone, Payload: "fills mailbox"}); err != nil {
		t.Fatalf("fill critical mailbox: %v", err)
	}

	reserved, release := installCriticalReserveHook(sub)
	result := make(chan error, 1)
	go func() {
		result <- b.PublishCritical(context.Background(), Event{Topic: TopicJobDone, Payload: "must not admit"})
	}()
	waitForCriticalWaiters(t, sub, 1)
	if got := receivePayload(t, ch); got != "fills mailbox" {
		t.Fatalf("first payload = %v, want fills mailbox", got)
	}
	waitForSignal(t, reserved, "publisher to reserve released capacity")

	closeReturned := make(chan struct{})
	go func() {
		b.Close()
		close(closeReturned)
	}()
	waitForSignal(t, b.done, "bus Close linearization")
	close(release)
	if err := receiveError(t, result); !errors.Is(err, ErrBusClosed) {
		t.Fatalf("PublishCritical error = %v, want ErrBusClosed", err)
	}
	waitForSignal(t, closeReturned, "Close to return")
}

func TestPublishRemainsNonblockingForStalledSubscriber(t *testing.T) {
	b := New()
	defer b.Close()
	_ = b.Subscribe(context.Background(), TopicStatsSample, 1)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 10_000 {
			b.Publish(Event{Topic: TopicStatsSample, Payload: i})
		}
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("best-effort Publish blocked behind a stalled subscriber")
	}
}

func TestLossySaturationCannotEvictCriticalEvent(t *testing.T) {
	b := New()
	defer b.Close()
	ch := b.Subscribe(context.Background(), TopicJobDone, 1)

	if err := b.PublishCritical(context.Background(), Event{Topic: TopicJobDone, Payload: "critical"}); err != nil {
		t.Fatalf("publish critical event: %v", err)
	}
	for i := range 100 {
		b.Publish(Event{Topic: TopicJobDone, Payload: i})
	}

	if got := receivePayload(t, ch); got != "critical" {
		t.Fatalf("first payload = %v, want critical", got)
	}
	if got := receivePayload(t, ch); got != 99 {
		t.Fatalf("retained lossy payload = %v, want 99", got)
	}
}

func TestPublishCriticalValidationAndCompatibilityHelper(t *testing.T) {
	b := New()
	defer b.Close()

	if err := b.PublishCritical(nil, Event{Topic: TopicJobDone}); !errors.Is(err, ErrNilContext) {
		t.Fatalf("nil-context error = %v, want ErrNilContext", err)
	}
	if err := b.PublishCritical(context.Background(), Event{}); !errors.Is(err, ErrTopicRequired) {
		t.Fatalf("empty-topic error = %v, want ErrTopicRequired", err)
	}

	legacy := legacyBus{}
	if err := PublishCritical(context.Background(), legacy, Event{Topic: TopicJobDone}); !errors.Is(err, ErrCriticalPublishUnsupported) {
		t.Fatalf("legacy helper error = %v, want ErrCriticalPublishUnsupported", err)
	}
}

func TestConcurrentPublishSubscribeAndCloseAreRaceSafe(t *testing.T) {
	for range 20 {
		b := New()
		ctx, cancel := context.WithCancel(context.Background())
		ch := b.Subscribe(ctx, TopicObjectsChanged, 4)

		var wg sync.WaitGroup
		criticalResult := make(chan error, 1)
		wg.Add(3)
		go func() {
			defer wg.Done()
			for i := range 100 {
				b.Publish(Event{Topic: TopicObjectsChanged, Payload: i})
			}
		}()
		go func() {
			defer wg.Done()
			publishCtx, cancelPublish := context.WithTimeout(context.Background(), time.Second)
			defer cancelPublish()
			for i := range 10 {
				if err := b.PublishCritical(publishCtx, Event{Topic: TopicObjectsChanged, Payload: i}); err != nil {
					criticalResult <- err
					return
				}
			}
			criticalResult <- nil
		}()
		go func() {
			defer wg.Done()
			for range ch {
			}
		}()

		cancel()
		b.Close()
		wg.Wait()
		if err := <-criticalResult; err != nil &&
			!errors.Is(err, ErrBusClosed) &&
			!errors.Is(err, ErrSubscriptionClosed) &&
			!errors.Is(err, context.Canceled) &&
			!errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("critical publisher returned unexpected error: %v", err)
		}
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

type legacyBus struct{}

func (legacyBus) Publish(Event) {}

func (legacyBus) Subscribe(context.Context, Topic, int) <-chan Event {
	ch := make(chan Event)
	close(ch)
	return ch
}

func onlySubscription(t *testing.T, b *MemoryBus, topic Topic) *subscription {
	t.Helper()
	b.mu.Lock()
	subs := b.subs[topic]
	if len(subs) != 1 {
		count := len(subs)
		b.mu.Unlock()
		t.Fatalf("subscriptions for %q = %d, want 1", topic, count)
	}
	var result *subscription
	for sub := range subs {
		result = sub
	}
	b.mu.Unlock()
	return result
}

func assertCriticalSlotsExhausted(t *testing.T, sub *subscription) {
	t.Helper()
	if available := len(sub.criticalSlots); available != 0 {
		t.Fatalf("available critical slots = %d, want 0", available)
	}
}

func assertCriticalSlotsAvailable(t *testing.T, sub *subscription, want int) {
	t.Helper()
	if available := len(sub.criticalSlots); available != want {
		t.Fatalf("available critical slots = %d, want %d", available, want)
	}
}

func waitForCriticalWaiters(t *testing.T, sub *subscription, want int32) {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		if got := sub.criticalWaiters.Load(); got == want {
			return
		}
		select {
		case <-sub.criticalWaiterChanged:
		case <-timer.C:
			t.Fatalf("critical waiters = %d, want %d", sub.criticalWaiters.Load(), want)
		}
	}
}

func installCriticalReserveHook(sub *subscription) (<-chan struct{}, chan<- struct{}) {
	reserved := make(chan struct{})
	release := make(chan struct{})
	sub.mailboxMu.Lock()
	sub.testAfterCriticalReserve = func() {
		close(reserved)
		<-release
	}
	sub.mailboxMu.Unlock()
	return reserved, release
}

func setLossyClaimHook(sub *subscription, hook func(*lossyEntry)) {
	sub.mailboxMu.Lock()
	sub.testAfterLossyClaim = hook
	sub.mailboxMu.Unlock()
}

func receiveLossyEntry(t *testing.T, entries <-chan *lossyEntry) *lossyEntry {
	t.Helper()
	select {
	case entry := <-entries:
		return entry
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for lossy claim")
		return nil
	}
}

func waitForSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func assertNoCriticalEntries(t *testing.T, sub *subscription) {
	t.Helper()
	sub.mailboxMu.Lock()
	count := len(sub.critical)
	sub.mailboxMu.Unlock()
	if count != 0 {
		t.Fatalf("critical mailbox entries = %d, want 0", count)
	}
}

func receiveError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for operation result")
		return nil
	}
}

func assertChannelClosed(t *testing.T, ch <-chan Event) {
	t.Helper()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("subscription channel remained open")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for subscription channel to close")
	}
}
