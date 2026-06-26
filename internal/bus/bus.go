package bus

import (
	"context"
	"sync"
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

type Event struct {
	Topic   Topic
	TS      time.Time
	Payload any
}

type Bus interface {
	Publish(Event)
	Subscribe(ctx context.Context, topic Topic, buf int) <-chan Event
}

type MemoryBus struct {
	mu     sync.Mutex
	now    func() time.Time
	subs   map[Topic]map[*subscription]struct{}
	closed bool
}

type subscription struct {
	ch chan Event
}

func New() *MemoryBus {
	return &MemoryBus{
		now:  func() time.Time { return time.Now().UTC() },
		subs: make(map[Topic]map[*subscription]struct{}),
	}
}

func (b *MemoryBus) Publish(event Event) {
	if event.Topic == "" {
		return
	}
	if event.TS.IsZero() {
		event.TS = b.now()
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}

	for sub := range b.subs[event.Topic] {
		deliverDropOldest(sub.ch, event)
	}
}

func (b *MemoryBus) Subscribe(ctx context.Context, topic Topic, buf int) <-chan Event {
	if buf < 1 {
		buf = 1
	}

	sub := &subscription{ch: make(chan Event, buf)}

	b.mu.Lock()
	if b.closed {
		close(sub.ch)
		b.mu.Unlock()
		return sub.ch
	}
	if b.subs[topic] == nil {
		b.subs[topic] = make(map[*subscription]struct{})
	}
	b.subs[topic][sub] = struct{}{}
	b.mu.Unlock()

	go func() {
		<-ctx.Done()
		b.unsubscribe(topic, sub)
	}()

	return sub.ch
}

func (b *MemoryBus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}
	b.closed = true
	for topic, subs := range b.subs {
		for sub := range subs {
			delete(subs, sub)
			close(sub.ch)
		}
		delete(b.subs, topic)
	}
}

func (b *MemoryBus) unsubscribe(topic Topic, sub *subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if subs := b.subs[topic]; subs != nil {
		if _, ok := subs[sub]; ok {
			delete(subs, sub)
			close(sub.ch)
		}
		if len(subs) == 0 {
			delete(b.subs, topic)
		}
	}
}

func deliverDropOldest(ch chan Event, event Event) {
	select {
	case ch <- event:
		return
	default:
	}

	select {
	case <-ch:
	default:
	}

	select {
	case ch <- event:
	default:
	}
}
