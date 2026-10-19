// Package events implements the in-process event bus used to broadcast realtime
// updates to the UI over SSE and to drive internal state transitions.
//
// The bus is intentionally simple: a bounded ring buffer of recent events plus
// a fan-out to subscribers. On a router this must not grow unbounded, so the
// buffer is capped and slow subscribers are dropped rather than blocking the
// producer.
package events

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/lanory/lanory/internal/model"
)

// Event is the envelope delivered to subscribers.
type Event struct {
	// Type is the SSE event name, e.g. "device.online".
	Type string `json:"type"`
	// Seq is a monotonically increasing sequence number, allowing clients to
	// detect gaps after a reconnect.
	Seq uint64 `json:"seq"`
	// Timestamp is when the event was emitted.
	Timestamp time.Time `json:"timestamp"`
	// Payload carries the event specific data.
	Payload any `json:"payload,omitempty"`
}

// DevicePayload is the payload of device.* events.
type DevicePayload struct {
	Device *model.Device `json:"device"`
	Reason string        `json:"reason,omitempty"`
}

// LinkPayload is the payload of link.* and topology.* events.
type LinkPayload struct {
	Edge   *model.TopologyEdge `json:"edge,omitempty"`
	Reason string              `json:"reason,omitempty"`
}

// ProbePayload is the payload of probe.* events.
type ProbePayload struct {
	Probe  *model.ProbeTarget `json:"probe,omitempty"`
	Result *model.ProbeResult `json:"result,omitempty"`
}

// DiscoveryPayload is the payload of discovery.* events.
type DiscoveryPayload struct {
	JobID   string `json:"job_id,omitempty"`
	Status  string `json:"status,omitempty"`
	Progress int   `json:"progress,omitempty"`
	Message string `json:"message,omitempty"`
}

// SSE event names. These strings are part of the API contract.
const (
	TypeDeviceUpdated  = "device.updated"
	TypeDeviceOnline   = "device.online"
	TypeDeviceOffline  = "device.offline"
	TypeTopologyUpdated = "topology.updated"
	TypeLinkUpdated    = "link.updated"
	TypeProbeCompleted = "probe.completed"
	TypeDiscoveryStarted   = "discovery.started"
	TypeDiscoveryCompleted = "discovery.completed"
	TypeSettingsUpdated    = "settings.updated"
)

// Bus is the event bus.
type Bus struct {
	mu       sync.RWMutex
	seq      uint64
	buffer   []Event
	capacity int
	subs     map[chan Event]struct{}
}

// New returns a Bus retaining the last capacity events for replay.
func New(capacity int) *Bus {
	if capacity <= 0 {
		capacity = 256
	}
	return &Bus{
		buffer:   make([]Event, 0, capacity),
		capacity: capacity,
		subs:     make(map[chan Event]struct{}),
	}
}

// Subscribe returns a channel that receives future events and a cancel func.
// The channel is buffered; when a subscriber falls behind it is closed rather
// than blocking the publisher.
func (b *Bus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	// Replay the retained buffer so a fresh client catches up quickly.
	replay := make([]Event, len(b.buffer))
	copy(replay, b.buffer)
	b.mu.Unlock()

	for _, e := range replay {
		select {
		case ch <- e:
		default:
		}
	}

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, ch)
			close(ch)
			b.mu.Unlock()
		})
	}
}

// Publish sends an event to all subscribers.
func (b *Bus) Publish(eventType string, payload any) Event {
	b.mu.Lock()
	b.seq++
	e := Event{
		Type:      eventType,
		Seq:       b.seq,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}
	if len(b.buffer) >= b.capacity {
		b.buffer = b.buffer[1:]
	}
	b.buffer = append(b.buffer, e)

	subs := make([]chan Event, 0, len(b.subs))
	for ch := range b.subs {
		subs = append(subs, ch)
	}
	b.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- e:
		default:
			// Slow subscriber: drop it instead of applying backpressure to the
			// discovery engine running on a router.
			b.mu.Lock()
			if _, ok := b.subs[ch]; ok {
				delete(b.subs, ch)
				close(ch)
			}
			b.mu.Unlock()
		}
	}
	return e
}

// PublishDeviceUpdated publishes a device update event.
func (b *Bus) PublishDeviceUpdated(d *model.Device, reason string) {
	b.Publish(TypeDeviceUpdated, DevicePayload{Device: d, Reason: reason})
}

// PublishDeviceOnline publishes a device online transition.
func (b *Bus) PublishDeviceOnline(d *model.Device, reason string) {
	b.Publish(TypeDeviceOnline, DevicePayload{Device: d, Reason: reason})
}

// PublishDeviceOffline publishes a device offline transition.
func (b *Bus) PublishDeviceOffline(d *model.Device, reason string) {
	b.Publish(TypeDeviceOffline, DevicePayload{Device: d, Reason: reason})
}

// PublishTopologyUpdated publishes a topology change.
func (b *Bus) PublishTopologyUpdated(reason string) {
	b.Publish(TypeTopologyUpdated, map[string]string{"reason": reason})
}

// PublishLinkUpdated publishes an edge change.
func (b *Bus) PublishLinkUpdated(e *model.TopologyEdge, reason string) {
	b.Publish(TypeLinkUpdated, LinkPayload{Edge: e, Reason: reason})
}

// PublishProbeCompleted publishes a probe result.
func (b *Bus) PublishProbeCompleted(p *model.ProbeTarget, r *model.ProbeResult) {
	b.Publish(TypeProbeCompleted, ProbePayload{Probe: p, Result: r})
}

// PublishDiscoveryStarted publishes the start of a discovery job.
func (b *Bus) PublishDiscoveryStarted(jobID, message string) {
	b.Publish(TypeDiscoveryStarted, DiscoveryPayload{JobID: jobID, Status: "running", Message: message})
}

// PublishDiscoveryCompleted publishes the completion of a discovery job.
func (b *Bus) PublishDiscoveryCompleted(jobID, status string, progress int) {
	b.Publish(TypeDiscoveryCompleted, DiscoveryPayload{JobID: jobID, Status: status, Progress: progress})
}

// PublishSettingsUpdated publishes a settings change.
func (b *Bus) PublishSettingsUpdated(key, value string) {
	b.Publish(TypeSettingsUpdated, map[string]string{"key": key, "value": value})
}

// Recent returns the retained events, oldest first.
func (b *Bus) Recent() []Event {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]Event, len(b.buffer))
	copy(out, b.buffer)
	return out
}

// MarshalJSON renders an SSE data frame for the event.
func (e Event) MarshalJSON() ([]byte, error) { return json.Marshal(e) }
