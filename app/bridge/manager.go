package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"broadlink-to-mqtt-gw/broadlink"
	"broadlink-to-mqtt-gw/config"
	"broadlink-to-mqtt-gw/hadiscovery"
	"broadlink-to-mqtt-gw/store"
	"github.com/philipparndt/go-logger"
)

// Remote is the subset of broadlink.Client that Device needs — extracted so
// tests can inject a fake without opening UDP sockets.
type Remote interface {
	Update(ctx context.Context) (broadlink.Info, error)
	SendData(ctx context.Context, code []byte) error
	Learn(ctx context.Context, wait time.Duration) ([]byte, error)
}

// repeatGap is the pause between repeated transmissions of one code.
const repeatGap = 100 * time.Millisecond

// Device is one configured remote plus the manager-side runtime state.
type Device struct {
	Cfg    config.Device
	Client Remote
	Source string // SourceConfig or SourceUI

	cancel context.CancelFunc // stops the poll loop; set when started

	learning atomic.Bool

	mu               sync.Mutex // guards the fields below
	info             broadlink.Info
	learn            LearnView
	discovered       bool
	lastStateJSON    []byte
	lastCommand      *string
	lastSent         *string
	targetTemp       *float64 // last known setpoint (climate commands)
	off              bool     // last command was OffCommand
	locked           bool
	available        bool
	availableInitial bool
}

// Manager owns the polling loop for every configured device and the routing
// of MQTT set/get/learn commands to the right device.
type Manager struct {
	cfg          config.Config
	basePrefix   string
	pollEvery    time.Duration
	learnTimeout time.Duration

	mu      sync.RWMutex
	devices map[string]*Device // keyed by config name
	order   []string           // config order, for stable listings
	store   *store.Store

	mqtt      MQTTClient
	discovery *hadiscovery.Publisher

	runCtx context.Context // set by Run; devices added later start under it
	wg     sync.WaitGroup

	// newRemote builds the client for a device added at runtime (replaced in tests).
	newRemote func(host string) Remote

	// discover finds Broadlink devices in the LAN (replaced in tests).
	discover func(context.Context, broadlink.DiscoverOptions) ([]broadlink.Found, error)
}

// NewManager builds a Manager with a real broadlink.Client per device. st
// holds the commands managed through the UI; nil means an in-memory store.
func NewManager(cfg config.Config, mqttClient MQTTClient, discovery *hadiscovery.Publisher, st *store.Store) *Manager {
	if st == nil {
		st = store.NewMemory()
	}
	m := &Manager{
		store:        st,
		cfg:          cfg,
		basePrefix:   cfg.MQTT.Topic,
		pollEvery:    time.Duration(cfg.PollingIntervalSeconds) * time.Second,
		learnTimeout: time.Duration(cfg.LearnTimeoutSeconds) * time.Second,
		devices:      make(map[string]*Device, len(cfg.Devices)),
		mqtt:         mqttClient,
		discovery:    discovery,
		discover:     broadlink.Discover,
		newRemote:    func(host string) Remote { return &broadlink.Client{Host: host} },
	}
	for _, d := range cfg.Devices {
		m.register(d)
	}
	// Devices added in the UI earlier; the config wins on a name clash.
	for _, name := range st.DeviceNames() {
		if _, taken := m.devices[name]; taken {
			logger.Warn("Ignoring UI device whose name is also in the config", "device", name)
			continue
		}
		m.register(config.Device{Name: name, Host: st.Devices()[name]}, SourceUI)
	}
	return m
}

// register adds a device (not yet started) and returns it. The source
// defaults to SourceConfig.
func (m *Manager) register(cfg config.Device, source ...string) *Device {
	d := &Device{Cfg: cfg, Client: &broadlink.Client{Host: cfg.Host}, Source: SourceConfig}
	if len(source) > 0 {
		d.Source = source[0]
	}
	m.mu.Lock()
	m.devices[cfg.Name] = d
	m.order = append(m.order, cfg.Name)
	m.mu.Unlock()
	return d
}

// start launches the poll loop of d under the manager's context.
func (m *Manager) start(d *Device) {
	ctx, cancel := context.WithCancel(m.runCtx)
	d.cancel = cancel
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.runDevice(ctx, d)
	}()
}

// NewManagerWith is the test-facing constructor that lets a suite inject
// fake Remotes per device name.
func NewManagerWith(cfg config.Config, mqttClient MQTTClient, discovery *hadiscovery.Publisher, st *store.Store, remotes map[string]Remote) *Manager {
	m := NewManager(cfg, mqttClient, discovery, st)
	for name, r := range remotes {
		if d, ok := m.devices[name]; ok {
			d.Client = r
		}
	}
	return m
}

// Run starts one goroutine per configured device and blocks until ctx is
// cancelled. Also installs the shared MQTT subscriptions for the command
// topics. Safe to call once per Manager instance.
func (m *Manager) Run(ctx context.Context) {
	for _, action := range []string{"set", "get", "learn"} {
		m.mqtt.Subscribe(fmt.Sprintf("%s/+/%s", m.basePrefix, action), m.handleMessage)
	}

	m.mu.Lock()
	m.runCtx = ctx
	for _, d := range m.devices {
		m.start(d)
	}
	m.mu.Unlock()

	<-ctx.Done()
	m.wg.Wait()
}

// runDevice is the per-device health-check loop. Backoff on error is
// exponential (1s→60s cap) and resets on the next successful poll.
func (m *Manager) runDevice(ctx context.Context, d *Device) {
	const (
		backoffBase = time.Second
		backoffMax  = 60 * time.Second
	)
	backoff := backoffBase

	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		if err := m.pollOnce(ctx, d); err != nil {
			if ctx.Err() != nil {
				return // stopped (shutdown or device removed): stay silent
			}
			m.publishAvailability(d, false)
			logger.Warn("Poll failed", "device", d.Cfg.Name, "error", err, "backoff", backoff)
			timer.Reset(backoff)
			if backoff < backoffMax {
				backoff = min(backoff*2, backoffMax)
			}
			continue
		}
		backoff = backoffBase
		m.publishAvailability(d, true)
		timer.Reset(m.pollEvery)
	}
}

// pollOnce runs one health check: asks the device for its info, triggers HA
// discovery on the first success and publishes the state on change. While a
// learning session is running the device is left alone.
func (m *Manager) pollOnce(ctx context.Context, d *Device) error {
	if d.learning.Load() {
		return nil
	}
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	info, err := d.Client.Update(pctx)
	if err != nil {
		return err
	}

	d.mu.Lock()
	first := !d.discovered
	d.discovered = true
	d.locked = info.Locked
	d.info = info
	d.mu.Unlock()

	if first {
		m.refreshDiscovery(d)
	}
	m.publishState(d, false)
	return nil
}

// publishState publishes the state payload; unless force is set, only when
// it differs from the previously published one.
func (m *Manager) publishState(d *Device, force bool) {
	d.mu.Lock()
	payload, err := json.Marshal(StatePayload{
		Locked:      d.locked,
		LastCommand: d.lastCommand,
		LastSent:    d.lastSent,
		Mode:        modeOf(d.off),
		Temperature: d.targetTemp,
	})
	if err != nil {
		d.mu.Unlock()
		logger.Error("Marshal state failed", "device", d.Cfg.Name, "error", err)
		return
	}
	if !force && string(payload) == string(d.lastStateJSON) {
		d.mu.Unlock()
		return
	}
	d.lastStateJSON = payload
	d.mu.Unlock()

	m.mqtt.Publish(fmt.Sprintf("%s/%s/state", m.basePrefix, d.Cfg.Name), payload, m.cfg.MQTT.Retain)
	logger.Debug("Published state", "device", d.Cfg.Name, "payload", string(payload))
}

// publishAvailability writes online/offline to `{prefix}/{name}/available`
// only when the availability state changes.
func (m *Manager) publishAvailability(d *Device, online bool) {
	d.mu.Lock()
	if d.availableInitial && d.available == online {
		d.mu.Unlock()
		return
	}
	d.available = online
	d.availableInitial = true
	d.mu.Unlock()

	value := "offline"
	if online {
		value = "online"
	}
	m.mqtt.Publish(fmt.Sprintf("%s/%s/available", m.basePrefix, d.Cfg.Name), []byte(value), m.cfg.MQTT.Retain)
	logger.Info("Device availability", "device", d.Cfg.Name, "state", value)
}

// handleMessage routes an incoming MQTT message on `{prefix}/{name}/{action}`
// to the right device. Unknown devices are logged at debug and dropped.
func (m *Manager) handleMessage(topic string, payload []byte) {
	name, action, ok := parseTopic(m.basePrefix, topic)
	if !ok {
		logger.Debug("Ignoring unmatched topic", "topic", topic)
		return
	}

	m.mu.RLock()
	d, exists := m.devices[name]
	m.mu.RUnlock()
	if !exists {
		logger.Debug("Ignoring message for unknown device", "device", name, "topic", topic)
		return
	}

	switch action {
	case "get":
		d.mu.Lock()
		cached := d.lastStateJSON
		d.mu.Unlock()
		if cached == nil {
			logger.Debug("Get before first poll — nothing cached", "device", name)
			return
		}
		m.mqtt.Publish(fmt.Sprintf("%s/%s/state", m.basePrefix, name), cached, m.cfg.MQTT.Retain)
	case "set":
		cmd, err := ParseSetCommand(payload)
		if err != nil {
			logger.Error("Invalid /set payload", "device", name, "error", err, "payload", string(payload))
			return
		}
		if err := m.Send(name, cmd); err != nil {
			logger.Error("Set failed", "device", name, "error", err)
		}
	case "learn":
		if err := m.StartLearn(name, ParseLearnHint(payload)); err != nil {
			logger.Warn("Learn not started", "device", name, "error", err)
		}
	}
}

// ParseLearnHint extracts an optional command name from a `/learn` payload
// (bare text, JSON string or {"command": "..."}) so the result can be
// labelled. Anything unusable yields "".
func ParseLearnHint(payload []byte) string {
	s := strings.TrimSpace(string(payload))
	if s == "" {
		return ""
	}
	if s[0] == '{' {
		var obj struct {
			Command string `json:"command"`
		}
		if json.Unmarshal([]byte(s), &obj) == nil {
			return obj.Command
		}
		return ""
	}
	if s[0] == '"' {
		var str string
		if json.Unmarshal([]byte(s), &str) == nil {
			s = str
		}
	}
	// Home Assistant's learn button sends the literal "learn".
	if s == "learn" {
		return ""
	}
	return s
}

// parseTopic splits `{prefix}/{name}/{action}` — returns (name, action, true)
// or ("","", false) if the topic does not match.
func parseTopic(prefix, topic string) (string, string, bool) {
	if !strings.HasPrefix(topic, prefix+"/") {
		return "", "", false
	}
	tail := topic[len(prefix)+1:]
	parts := strings.SplitN(tail, "/", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// modeOf is the state-topic mode: only "off" is known, the unit does not report anything back.
func modeOf(off bool) string {
	if off {
		return "off"
	}
	return ""
}
