package bus

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

type Topic string

const (
	TopicAppReady                Topic = "app:ready"
	TopicProviderChanged         Topic = "provider:changed"
	TopicProviderStatus          Topic = "provider:status"
	TopicProviderInstallProgress Topic = "provider:install:progress"
	TopicDockerConnected         Topic = "docker:connected"
	TopicDockerReconnecting      Topic = "docker:reconnecting"
	TopicDockerDisconnected      Topic = "docker:disconnected"
	TopicObjectsChanged          Topic = "objects:changed"
	TopicProjectChanged          Topic = "project:changed"
	TopicStatsSample             Topic = "stats:sample"
	TopicLogsLines               Topic = "logs:lines"
	TopicLogsEOF                 Topic = "logs:eof"
	TopicLogsError               Topic = "logs:error"
	TopicTerminalData            Topic = "terminal:data"
	TopicTerminalClosed          Topic = "terminal:closed"
	TopicImagePullProgress       Topic = "image:pull:progress"
	TopicImagePushProgress       Topic = "image:push:progress"
	TopicUpdatesCheckProgress    Topic = "updates:check:progress"
	TopicUpdatesApplied          Topic = "updates:applied"
	TopicJobProgress             Topic = "job:progress"
	TopicJobDone                 Topic = "job:done"
	TopicNotification            Topic = "notification"
	TopicPortForwardChanged      Topic = "portforward:changed"
)

var (
	// ErrBusClosed means a critical event could not be admitted because the bus
	// was closed. Ordinary Publish intentionally remains a best-effort no-op
	// after Close.
	ErrBusClosed = errors.New("bus: closed")
	// ErrSubscriptionClosed means a subscriber that was present in the
	// publication snapshot ended before it admitted the critical event.
	ErrSubscriptionClosed = errors.New("bus: subscription closed")
	// ErrCriticalPublishUnsupported means the supplied Bus only implements the
	// original best-effort publication contract.
	ErrCriticalPublishUnsupported = errors.New("bus: critical publishing is unsupported")
	// ErrTopicRequired reports a malformed critical event. Best-effort Publish
	// keeps its historical behavior of silently ignoring these events.
	ErrTopicRequired = errors.New("bus: event topic is required")
	// ErrNilContext reports a nil context passed to PublishCritical.
	ErrNilContext = errors.New("bus: nil context")
)

type Event struct {
	Topic   Topic
	TS      time.Time
	Payload any
}

// Bus is the original best-effort event bus contract. Implementations are not
// required to support reliable publication.
type Bus interface {
	Publish(Event)
	Subscribe(ctx context.Context, topic Topic, buf int) <-chan Event
}

// CriticalPublisher admits events to a separate bounded reliable mailbox.
// Success means that every subscriber in the publication snapshot admitted
// the event; it does not mean that downstream consumers processed it. If an
// error is returned, earlier subscribers in the snapshot may already have
// admitted the event.
type CriticalPublisher interface {
	PublishCritical(ctx context.Context, event Event) error
}

// PublishCritical uses the reliable extension when publisher supports it. It
// never silently degrades a critical event to best-effort publication.
func PublishCritical(ctx context.Context, publisher Bus, event Event) error {
	critical, ok := publisher.(CriticalPublisher)
	if !ok {
		return ErrCriticalPublishUnsupported
	}
	return critical.PublishCritical(ctx, event)
}

type MemoryBus struct {
	admissionMu   sync.RWMutex
	mu            sync.Mutex
	now           func() time.Time
	subs          map[Topic]map[*subscription]struct{}
	done          chan struct{}
	closeComplete chan struct{}
	cleanup       sync.WaitGroup
	closed        bool
}

// subscription owns out, including its closure. Producers only touch the two
// private mailboxes, so subscription cancellation and bus shutdown cannot race
// a send against closing the public channel.
type subscription struct {
	out     chan Event
	ctxDone <-chan struct{}
	stopped chan struct{}
	wake    chan struct{}

	mailboxMu     sync.Mutex
	active        bool
	lossyCapacity int
	lossy         []*lossyEntry
	critical      []*criticalEntry
	criticalSlots chan struct{}
	lossyClaim    *lossyEntry

	criticalWaiters       atomic.Int32
	criticalWaiterChanged chan struct{}

	// These hooks are nil outside white-box tests. They expose the exact state
	// transitions needed to deterministically exercise admission races.
	testAfterLossyClaim      func(*lossyEntry)
	testAfterCriticalReserve func()
}

type lossyEntryState uint8

const (
	lossyQueued lossyEntryState = iota
	lossyClaimed
	lossyInvalidated
	lossyDelivered
	lossyAbandoned
)

type lossyEntry struct {
	event       Event
	state       lossyEntryState
	claimed     chan struct{}
	invalidated chan struct{}
}

type criticalEntry struct {
	event Event
}

func New() *MemoryBus {
	return &MemoryBus{
		now:           func() time.Time { return time.Now().UTC() },
		subs:          make(map[Topic]map[*subscription]struct{}),
		done:          make(chan struct{}),
		closeComplete: make(chan struct{}),
	}
}

// Publish is nonblocking. Each subscription's pending mailbox retains only its
// newest buf best-effort events; an entry already claimed by the worker is
// irrevocably in flight. Lossy events can never displace admitted critical
// events.
func (b *MemoryBus) Publish(event Event) {
	if event.Topic == "" {
		return
	}
	event = b.timestamp(event)

	subs, closed := b.snapshot(event.Topic)
	if closed {
		return
	}
	for _, sub := range subs {
		sub.publishLossy(event)
	}
}

// PublishCritical waits for bounded reliable-mailbox capacity. Waiting occurs
// after the subscriber snapshot is taken and without holding the bus mutex.
func (b *MemoryBus) PublishCritical(ctx context.Context, event Event) error {
	if ctx == nil {
		return ErrNilContext
	}
	if event.Topic == "" {
		return ErrTopicRequired
	}
	event = b.timestamp(event)

	subs, closed := b.snapshot(event.Topic)
	if closed {
		return ErrBusClosed
	}
	for _, sub := range subs {
		if err := sub.publishCritical(ctx, b, event); err != nil {
			return err
		}
	}
	return nil
}

func (b *MemoryBus) Subscribe(ctx context.Context, topic Topic, buf int) <-chan Event {
	if ctx == nil {
		ctx = context.Background()
	}
	if buf < 1 {
		buf = 1
	}

	sub := newSubscription(ctx, buf)

	b.mu.Lock()
	if b.closed {
		close(sub.out)
		b.mu.Unlock()
		return sub.out
	}
	if b.subs[topic] == nil {
		b.subs[topic] = make(map[*subscription]struct{})
	}
	b.subs[topic][sub] = struct{}{}
	b.cleanup.Add(1)
	b.mu.Unlock()

	go b.runSubscription(topic, sub)
	return sub.out
}

func newSubscription(ctx context.Context, capacity int) *subscription {
	sub := &subscription{
		out:                   make(chan Event),
		ctxDone:               ctx.Done(),
		stopped:               make(chan struct{}),
		wake:                  make(chan struct{}, 1),
		active:                true,
		lossyCapacity:         capacity,
		lossy:                 make([]*lossyEntry, 0, capacity),
		critical:              make([]*criticalEntry, 0, capacity),
		criticalSlots:         make(chan struct{}, capacity),
		criticalWaiterChanged: make(chan struct{}, 1),
	}
	for range capacity {
		sub.criticalSlots <- struct{}{}
	}
	return sub
}

func (b *MemoryBus) Close() {
	// Admission holds the read side only for its final checks and mailbox
	// append. Taking the write side here gives Close a linearization point
	// against successful critical admission without extending the barrier over
	// worker cleanup.
	b.admissionMu.Lock()
	b.mu.Lock()
	if b.closed {
		closeComplete := b.closeComplete
		b.mu.Unlock()
		b.admissionMu.Unlock()
		<-closeComplete
		return
	}
	b.closed = true
	close(b.done)
	// Workers own their output channels. Clearing the registry prevents new
	// snapshots while b.done wakes every existing worker and blocked publisher.
	b.subs = make(map[Topic]map[*subscription]struct{})
	b.mu.Unlock()
	b.admissionMu.Unlock()

	b.cleanup.Wait()
	close(b.closeComplete)
}

func (b *MemoryBus) snapshot(topic Topic) ([]*subscription, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, true
	}
	subs := make([]*subscription, 0, len(b.subs[topic]))
	for sub := range b.subs[topic] {
		subs = append(subs, sub)
	}
	return subs, false
}

func (b *MemoryBus) timestamp(event Event) Event {
	if event.TS.IsZero() {
		event.TS = b.now()
	}
	return event
}

func (b *MemoryBus) runSubscription(topic Topic, sub *subscription) {
	defer b.cleanup.Done()
	defer func() {
		b.removeSubscription(topic, sub)
		sub.stop()
		close(sub.stopped)
		close(sub.out)
	}()

	for {
		sub.drainWake()
		critical, lossy := sub.next()
		switch {
		case critical != nil:
			select {
			case sub.out <- critical.event:
				sub.removeCritical(critical)
			case <-sub.ctxDone:
				return
			case <-b.done:
				return
			}
		case lossy != nil:
			select {
			case sub.out <- lossy.event:
				sub.removeLossy(lossy)
			case <-sub.wake:
			case <-sub.ctxDone:
				return
			case <-b.done:
				return
			}
		default:
			select {
			case <-sub.wake:
			case <-sub.ctxDone:
				return
			case <-b.done:
				return
			}
		}
	}
}

func (b *MemoryBus) removeSubscription(topic Topic, sub *subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if subs := b.subs[topic]; subs != nil {
		delete(subs, sub)
		if len(subs) == 0 {
			delete(b.subs, topic)
		}
	}
}

func (sub *subscription) publishLossy(event Event) {
	entry := &lossyEntry{
		event:       event,
		state:       lossyQueued,
		claimed:     make(chan struct{}),
		invalidated: make(chan struct{}),
	}

	sub.mailboxMu.Lock()
	defer sub.mailboxMu.Unlock()
	if !sub.active {
		return
	}
	if len(sub.lossy) == sub.lossyCapacity {
		// Only queued entries are evictable. next removes an entry from this
		// slice and marks it claimed under the same mutex before the worker can
		// attempt delivery.
		oldest := sub.lossy[0]
		sub.lossy = sub.lossy[1:]
		oldest.state = lossyInvalidated
		close(oldest.invalidated)
	}
	sub.lossy = append(sub.lossy, entry)
	sub.notify()
}

func (sub *subscription) publishCritical(ctx context.Context, bus *MemoryBus, event Event) error {
	if err := sub.criticalAbort(ctx, bus.done); err != nil {
		return err
	}
	if err := sub.reserveCriticalSlot(ctx, bus.done); err != nil {
		return err
	}
	reserved := true
	defer func() {
		if reserved {
			sub.releaseCriticalSlot()
		}
	}()

	sub.afterCriticalReserve()
	if err := sub.criticalAbort(ctx, bus.done); err != nil {
		return err
	}

	// Close needs the write side only while it closes bus.done. Therefore no
	// successful append can straddle the Close linearization point, and no
	// capacity wait occurs while either the bus mutex or this barrier is held.
	bus.admissionMu.RLock()
	defer bus.admissionMu.RUnlock()
	if err := sub.criticalAbort(ctx, bus.done); err != nil {
		return err
	}

	sub.mailboxMu.Lock()
	defer sub.mailboxMu.Unlock()
	if err := sub.criticalAbort(ctx, bus.done); err != nil {
		return err
	}
	if !sub.active {
		return ErrSubscriptionClosed
	}
	sub.critical = append(sub.critical, &criticalEntry{event: event})
	sub.notify()
	reserved = false
	return nil
}

func (sub *subscription) next() (*criticalEntry, *lossyEntry) {
	sub.mailboxMu.Lock()
	if len(sub.critical) > 0 {
		entry := sub.critical[0]
		sub.mailboxMu.Unlock()
		return entry, nil
	}
	if sub.lossyClaim != nil {
		entry := sub.lossyClaim
		sub.mailboxMu.Unlock()
		return nil, entry
	}
	if len(sub.lossy) > 0 {
		entry := sub.lossy[0]
		sub.lossy = sub.lossy[1:]
		// This is the lossy-delivery linearization point. Because eviction uses
		// mailboxMu too, an entry is either invalidated while queued or claimed
		// for delivery, never both.
		entry.state = lossyClaimed
		sub.lossyClaim = entry
		close(entry.claimed)
		hook := sub.testAfterLossyClaim
		sub.mailboxMu.Unlock()
		if hook != nil {
			hook(entry)
		}
		return nil, entry
	}
	sub.mailboxMu.Unlock()
	return nil, nil
}

func (sub *subscription) removeCritical(target *criticalEntry) {
	sub.mailboxMu.Lock()
	if len(sub.critical) > 0 && sub.critical[0] == target {
		sub.critical = sub.critical[1:]
		sub.mailboxMu.Unlock()
		sub.releaseCriticalSlot()
		return
	}
	sub.mailboxMu.Unlock()
}

func (sub *subscription) removeLossy(target *lossyEntry) {
	sub.mailboxMu.Lock()
	if sub.lossyClaim == target && target.state == lossyClaimed {
		target.state = lossyDelivered
		sub.lossyClaim = nil
	}
	sub.mailboxMu.Unlock()
}

func (sub *subscription) stop() {
	sub.mailboxMu.Lock()
	sub.active = false
	for _, entry := range sub.lossy {
		entry.state = lossyInvalidated
		close(entry.invalidated)
	}
	sub.lossy = nil
	if sub.lossyClaim != nil {
		sub.lossyClaim.state = lossyAbandoned
		sub.lossyClaim = nil
	}
	sub.critical = nil
	sub.mailboxMu.Unlock()
}

func (sub *subscription) reserveCriticalSlot(ctx context.Context, busDone <-chan struct{}) error {
	if err := sub.criticalAbort(ctx, busDone); err != nil {
		return err
	}
	select {
	case <-sub.criticalSlots:
		return nil
	default:
	}

	sub.criticalWaiters.Add(1)
	sub.notifyCriticalWaiterChanged()
	defer func() {
		sub.criticalWaiters.Add(-1)
		sub.notifyCriticalWaiterChanged()
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-busDone:
		return ErrBusClosed
	case <-sub.ctxDone:
		return ErrSubscriptionClosed
	case <-sub.stopped:
		return ErrSubscriptionClosed
	case <-sub.criticalSlots:
		return nil
	}
}

func (sub *subscription) criticalAbort(ctx context.Context, busDone <-chan struct{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-busDone:
		return ErrBusClosed
	default:
	}
	select {
	case <-sub.ctxDone:
		return ErrSubscriptionClosed
	default:
	}
	select {
	case <-sub.stopped:
		return ErrSubscriptionClosed
	default:
	}
	return nil
}

func (sub *subscription) afterCriticalReserve() {
	sub.mailboxMu.Lock()
	hook := sub.testAfterCriticalReserve
	sub.mailboxMu.Unlock()
	if hook != nil {
		hook()
	}
}

func (sub *subscription) notifyCriticalWaiterChanged() {
	select {
	case sub.criticalWaiterChanged <- struct{}{}:
	default:
	}
}

func (sub *subscription) notify() {
	select {
	case sub.wake <- struct{}{}:
	default:
	}
}

func (sub *subscription) drainWake() {
	select {
	case <-sub.wake:
	default:
	}
}

func (sub *subscription) releaseCriticalSlot() {
	sub.criticalSlots <- struct{}{}
}
