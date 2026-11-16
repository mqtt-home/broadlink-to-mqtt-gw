package hadiscovery

import (
	"sort"
	"sync"

	"broadlink-to-mqtt-gw/broadlink"
	"github.com/philipparndt/go-logger"
)

// Publisher owns the Home Assistant MQTT discovery lifecycle: it publishes
// retained discovery configs for each known device and clears them on
// graceful shutdown. Safe for concurrent use from multiple device goroutines.
type Publisher struct {
	basePrefix string
	pub        func(topic string, payload []byte, retain bool)

	mu     sync.Mutex
	topics map[string]map[string]struct{} // device name -> topics we have published
}

// NewPublisher builds a Publisher that writes to the given publish function.
// `basePrefix` is the MQTT topic prefix used by the device topics.
func NewPublisher(basePrefix string, publish func(topic string, payload []byte, retain bool)) *Publisher {
	return &Publisher{
		basePrefix: basePrefix,
		pub:        publish,
		topics:     make(map[string]map[string]struct{}),
	}
}

// Publish emits the discovery configs for one device: one button per
// command plus a "Learn" button. Idempotent; entities of commands that are
// no longer listed are removed again.
func (p *Publisher) Publish(deviceName string, info broadlink.Info, commands []string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	sorted := append([]string(nil), commands...)
	sort.Strings(sorted)

	current := make(map[string]struct{}, len(sorted)+1)
	emit := func(key, label, action, payload, category string) {
		topic, body, err := ButtonConfig(p.basePrefix, deviceName, info, key, label, action, payload, category)
		if err != nil {
			logger.Error("HA discovery: button config marshal failed", "device", deviceName, "entity", key, "error", err)
			return
		}
		p.pub(topic, body, true)
		current[topic] = struct{}{}
	}
	for _, cmd := range sorted {
		emit(cmd, cmd, "set", cmd, "")
	}
	emit("learn", "Learn", "learn", "learn", "config")

	for topic := range p.topics[deviceName] {
		if _, keep := current[topic]; !keep {
			p.pub(topic, []byte(""), true)
		}
	}
	p.topics[deviceName] = current
}

// Cleanup clears every discovery topic ever published by this Publisher
// (empty retained payload → HA removes the entity). Called from the graceful
// shutdown path before the MQTT client disconnects.
func (p *Publisher) Cleanup() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, topics := range p.topics {
		for topic := range topics {
			p.pub(topic, []byte(""), true)
		}
	}
	p.topics = make(map[string]map[string]struct{})
}

// TrackedTopics returns a snapshot of the currently tracked discovery topics.
// Test-only surface — not for callers to depend on in production.
func (p *Publisher) TrackedTopics() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, topics := range p.topics {
		for t := range topics {
			out = append(out, t)
		}
	}
	return out
}
