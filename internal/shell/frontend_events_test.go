package shell

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/bus"
)

var intendedFrontendEvents = map[bus.Topic]string{
	bus.TopicProviderChanged:         "provider:changed",
	bus.TopicDockerConnected:         "docker:connected",
	bus.TopicDockerReconnecting:      "docker:reconnecting",
	bus.TopicDockerDisconnected:      "docker:disconnected",
	bus.TopicObjectsChanged:          "objects:changed",
	bus.TopicProjectChanged:          "project:changed",
	bus.TopicProviderInstallProgress: "provider:install:progress",
	bus.TopicImagePullProgress:       "image:pull:progress",
	bus.TopicImagePushProgress:       "image:push:progress",
	bus.TopicUpdatesCheckProgress:    "updates:check:progress",
	bus.TopicUpdatesApplied:          "updates:applied",
	bus.TopicLogsLines:               "logs:lines",
	bus.TopicLogsEOF:                 "logs:eof",
	bus.TopicLogsError:               "logs:error",
	bus.TopicTerminalData:            "terminal:data",
	bus.TopicTerminalClosed:          "terminal:closed",
	bus.TopicStatsSample:             "stats:sample",
	bus.TopicJobProgress:             "job:progress",
	bus.TopicJobDone:                 "job:done",
	bus.TopicNotification:            "notification",
	bus.TopicPortForwardChanged:      "portforward:changed",
}

func TestFrontendEventRoutesMatchPublicContract(t *testing.T) {
	routes := bus.FrontendEventRoutes()
	if len(routes) != len(intendedFrontendEvents) {
		t.Fatalf("frontend event route count = %d, want %d", len(routes), len(intendedFrontendEvents))
	}

	seenTopics := make(map[bus.Topic]struct{}, len(routes))
	seenNames := make(map[string]bus.Topic, len(routes))
	for _, route := range routes {
		wantName, ok := intendedFrontendEvents[route.Topic]
		if !ok {
			t.Errorf("unexpected frontend bus topic %q", route.Topic)
			continue
		}
		if route.EventName != wantName {
			t.Errorf("frontend event name for %q = %q, want %q", route.Topic, route.EventName, wantName)
		}
		if _, duplicate := seenTopics[route.Topic]; duplicate {
			t.Errorf("frontend bus topic %q is declared more than once", route.Topic)
		}
		seenTopics[route.Topic] = struct{}{}
		if otherTopic, duplicate := seenNames[route.EventName]; duplicate {
			t.Errorf("frontend event name %q is shared by topics %q and %q", route.EventName, otherTopic, route.Topic)
		}
		seenNames[route.EventName] = route.Topic
	}

	for topic, eventName := range intendedFrontendEvents {
		if _, ok := seenTopics[topic]; !ok {
			t.Errorf("public topic %q is not forwarded as frontend event %q", topic, eventName)
		}
	}
}

func TestForwardBusEventsUsesDeclaredFrontendNamesAndPayloads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventBus := bus.New()
	defer eventBus.Close()
	emitter := &recordingEventEmitter{events: make(chan emittedEvent, len(intendedFrontendEvents))}
	routes := bus.FrontendEventRoutes()
	forwardBusEvents(ctx, eventBus, emitter, routes)

	for _, route := range routes {
		eventBus.Publish(bus.Event{Topic: route.Topic, Payload: fmt.Sprintf("payload:%s", route.Topic)})
	}

	received := make(map[string]any, len(routes))
	for range routes {
		select {
		case event := <-emitter.events:
			received[event.name] = event.payload
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out after receiving %d of %d forwarded events", len(received), len(routes))
		}
	}

	for topic, eventName := range intendedFrontendEvents {
		wantPayload := fmt.Sprintf("payload:%s", topic)
		if got := received[eventName]; got != wantPayload {
			t.Errorf("frontend event %q payload = %#v, want %q", eventName, got, wantPayload)
		}
	}
}

type emittedEvent struct {
	name    string
	payload any
}

type recordingEventEmitter struct {
	events chan emittedEvent
}

func (e *recordingEventEmitter) EmitEvent(name string, data ...any) bool {
	var payload any
	if len(data) > 0 {
		payload = data[0]
	}
	e.events <- emittedEvent{name: name, payload: payload}
	return true
}
