// Package bus is a small in-process publish/subscribe hub. GitHub webhook
// deliveries are published on topics such as "pr:owner/repo#42" and
// "user:login"; open Server-Sent Event streams subscribe to the topics they
// render and re-render when an event arrives.
//
// Subscribers are never blocked on: each subscription has a small buffer
// and events are dropped when it is full. A dropped event is harmless
// because every event only says "something changed, re-fetch"; the next
// event or the periodic refresh catches up.
package bus

import (
	"sync"
	"time"
)

// Event is a change notification.
type Event struct {
	Topic string
	// Kind is the webhook event name (pull_request, check_run, ...).
	Kind string
	// Action is the webhook action (opened, synchronize, ...).
	Action string
	At     time.Time
}

// Topic builders.
func PRTopic(owner, repo string, number int) string {
	return "pr:" + owner + "/" + repo + "#" + itoa(number)
}

func UserTopic(login string) string { return "user:" + login }

func SHATopic(sha string) string { return "sha:" + sha }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

type subscriber struct {
	ch     chan Event
	topics map[string]struct{}
}

// Bus fans events out to subscribers.
type Bus struct {
	mu   sync.RWMutex
	subs map[*subscriber]struct{}
	// counters for observability
	published, dropped uint64
}

// New creates a Bus.
func New() *Bus {
	return &Bus{subs: make(map[*subscriber]struct{})}
}

// Subscribe returns a channel that receives events for any of the topics
// and a cancel function that must be called when done.
func (b *Bus) Subscribe(topics ...string) (<-chan Event, func()) {
	s := &subscriber{ch: make(chan Event, 16), topics: make(map[string]struct{}, len(topics))}
	for _, t := range topics {
		s.topics[t] = struct{}{}
	}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	var once sync.Once
	return s.ch, func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, s)
			b.mu.Unlock()
		})
	}
}

// Publish delivers ev to every subscriber of ev.Topic without blocking.
func (b *Bus) Publish(ev Event) {
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.published++
	for s := range b.subs {
		if _, ok := s.topics[ev.Topic]; !ok {
			continue
		}
		select {
		case s.ch <- ev:
		default:
			b.dropped++
		}
	}
}

// Stats returns subscriber count, published and dropped totals.
func (b *Bus) Stats() (subscribers int, published, dropped uint64) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs), b.published, b.dropped
}
