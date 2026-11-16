package bridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"broadlink-to-mqtt-gw/config"
	"broadlink-to-mqtt-gw/store"
	"github.com/philipparndt/go-logger"
)

// Errors returned by the operations below; the HTTP layer maps them to
// status codes.
var (
	ErrNotFound = errors.New("not found")
	ErrReadOnly = errors.New("command is defined in the config file and cannot be changed here")
	ErrExists   = errors.New("command already exists")
	ErrInvalid  = errors.New("invalid request")
	ErrBusy     = errors.New("device is busy (learning in progress)")
)

// MaxCodeBytes caps the size of a stored IR code.
const MaxCodeBytes = 8192

// Command sources.
const (
	SourceConfig = "config" // read-only, from the config file
	SourceUI     = "ui"     // managed through the UI / API
)

// CommandView describes one command of a device.
type CommandView struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Bytes  int    `json:"bytes"`
	Code   string `json:"code"` // base64
}

// LearnView is the state of the (last) learning session of a device.
type LearnView struct {
	State   string `json:"state"` // idle | learning | done | error
	Hint    string `json:"hint,omitempty"`
	Code    string `json:"code,omitempty"`
	Error   string `json:"error,omitempty"`
	Started string `json:"started,omitempty"`
	Timeout int    `json:"timeout_seconds,omitempty"`
}

// DeviceView is the UI-facing snapshot of one remote.
type DeviceView struct {
	Name        string        `json:"name"`
	Host        string        `json:"host"`
	Model       string        `json:"model,omitempty"`
	MAC         string        `json:"mac,omitempty"`
	Online      bool          `json:"online"`
	Locked      bool          `json:"locked"`
	LastCommand *string       `json:"last_command"`
	LastSent    *string       `json:"last_sent"`
	Learn       LearnView     `json:"learn"`
	Commands    []CommandView `json:"commands"`
}

func (m *Manager) device(name string) (*Device, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	d, ok := m.devices[name]
	if !ok {
		return nil, fmt.Errorf("%w: device %q", ErrNotFound, name)
	}
	return d, nil
}

// lookup resolves a command name to its code; config commands win.
func (m *Manager) lookup(d *Device, name string) ([]byte, string, bool) {
	if c, ok := d.Cfg.Codes[name]; ok {
		return c, SourceConfig, true
	}
	if c, ok := m.store.Get(d.Cfg.Name, name); ok {
		return c, SourceUI, true
	}
	return nil, "", false
}

func (m *Manager) commandViews(d *Device) []CommandView {
	var out []CommandView
	for n, c := range d.Cfg.Codes {
		out = append(out, CommandView{Name: n, Source: SourceConfig, Bytes: len(c), Code: base64.StdEncoding.EncodeToString(c)})
	}
	for _, n := range m.store.Names(d.Cfg.Name) {
		if _, shadowed := d.Cfg.Codes[n]; shadowed {
			continue
		}
		c, _ := m.store.Get(d.Cfg.Name, n)
		out = append(out, CommandView{Name: n, Source: SourceUI, Bytes: len(c), Code: base64.StdEncoding.EncodeToString(c)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (m *Manager) commandNames(d *Device) []string {
	views := m.commandViews(d)
	names := make([]string, len(views))
	for i, v := range views {
		names[i] = v.Name
	}
	return names
}

// refreshDiscovery (re)publishes the HA entities once the device is known.
func (m *Manager) refreshDiscovery(d *Device) {
	d.mu.Lock()
	known, info := d.discovered, d.info
	d.mu.Unlock()
	if known && m.discovery != nil {
		m.discovery.Publish(d.Cfg.Name, info, m.commandNames(d))
	}
}

// ListDevices returns a snapshot of all devices in config order.
func (m *Manager) ListDevices() []DeviceView {
	out := make([]DeviceView, 0, len(m.order))
	for _, name := range m.order {
		d, err := m.device(name)
		if err != nil {
			continue
		}
		d.mu.Lock()
		v := DeviceView{
			Name: name, Host: d.Cfg.Host, Model: d.info.Model, MAC: d.info.MAC,
			Online: d.available, Locked: d.locked,
			LastCommand: d.lastCommand, LastSent: d.lastSent,
			Learn: d.learn,
		}
		d.mu.Unlock()
		v.Commands = m.commandViews(d)
		if v.Commands == nil {
			v.Commands = []CommandView{}
		}
		if v.Learn.State == "" {
			v.Learn.State = "idle"
		}
		out = append(out, v)
	}
	return out
}

// Send transmits a configured command or a raw code (cmd.Repeat times).
func (m *Manager) Send(deviceName string, cmd SetCommand) error {
	d, err := m.device(deviceName)
	if err != nil {
		return err
	}
	code, label := cmd.Code, "(raw code)"
	if cmd.Name != "" {
		c, _, ok := m.lookup(d, cmd.Name)
		if !ok {
			return fmt.Errorf("%w: command %q", ErrNotFound, cmd.Name)
		}
		code, label = c, cmd.Name
	}
	if len(code) == 0 || len(code) > MaxCodeBytes {
		return fmt.Errorf("%w: code must be 1-%d bytes", ErrInvalid, MaxCodeBytes)
	}
	repeat := max(cmd.Repeat, 1)
	if repeat > MaxRepeat {
		return fmt.Errorf("%w: repeat must be 1-%d", ErrInvalid, MaxRepeat)
	}
	if d.learning.Load() {
		return ErrBusy
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i := 0; i < repeat; i++ {
		if i > 0 {
			time.Sleep(repeatGap)
		}
		if err := d.Client.SendData(ctx, code); err != nil {
			return fmt.Errorf("send %s: %w", label, err)
		}
	}
	logger.Info("Sent command", "device", deviceName, "command", label, "repeat", repeat)

	now := time.Now().UTC().Format(time.RFC3339)
	d.mu.Lock()
	d.lastCommand = &label
	d.lastSent = &now
	d.mu.Unlock()
	m.publishState(d, true)
	return nil
}

// StartLearn begins an asynchronous learning session (one at a time per
// device). The result is available through LearnStatus and on MQTT.
func (m *Manager) StartLearn(deviceName, hint string) error {
	d, err := m.device(deviceName)
	if err != nil {
		return err
	}
	if !d.learning.CompareAndSwap(false, true) {
		return fmt.Errorf("%w: learning already in progress", ErrBusy)
	}
	d.mu.Lock()
	d.learn = LearnView{
		State: "learning", Hint: hint, Timeout: int(m.learnTimeout.Seconds()),
		Started: time.Now().UTC().Format(time.RFC3339),
	}
	d.mu.Unlock()
	// Learning can take tens of seconds — never block the caller.
	go m.learnSession(d, hint)
	return nil
}

// LearnStatus returns the state of the latest learning session.
func (m *Manager) LearnStatus(deviceName string) (LearnView, error) {
	d, err := m.device(deviceName)
	if err != nil {
		return LearnView{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	v := d.learn
	if v.State == "" {
		v.State = "idle"
	}
	return v, nil
}

// learnSession runs one IR learning session and publishes the result (or the
// error) to `{prefix}/{name}/learned`. The caller has already set d.learning.
func (m *Manager) learnSession(d *Device, hint string) {
	defer d.learning.Store(false)
	name := d.Cfg.Name
	topic := fmt.Sprintf("%s/%s/learned", m.basePrefix, name)

	ctx, cancel := context.WithTimeout(context.Background(), m.learnTimeout+10*time.Second)
	defer cancel()

	logger.Info("Learning started — press a button on the remote", "device", name, "timeout", m.learnTimeout)
	code, err := d.Client.Learn(ctx, m.learnTimeout)
	if err != nil {
		logger.Error("Learning failed", "device", name, "error", err)
		d.mu.Lock()
		d.learn = LearnView{State: "error", Hint: hint, Error: err.Error()}
		d.mu.Unlock()
		out, _ := json.Marshal(map[string]string{"error": err.Error()})
		m.mqtt.Publish(topic, out, false)
		return
	}

	encoded := base64.StdEncoding.EncodeToString(code)
	logger.Info("Learned code", "device", name, "command", hint, "code", encoded)
	d.mu.Lock()
	d.learn = LearnView{State: "done", Hint: hint, Code: encoded}
	d.mu.Unlock()

	result := map[string]string{
		"code":       encoded,
		"learned_at": time.Now().UTC().Format(time.RFC3339),
	}
	if hint != "" {
		result["command"] = hint
	}
	out, _ := json.Marshal(result)
	m.mqtt.Publish(topic, out, m.cfg.MQTT.Retain)
}

// SaveCommand creates or overwrites a UI-managed command.
func (m *Manager) SaveCommand(deviceName, name string, code []byte) error {
	d, err := m.device(deviceName)
	if err != nil {
		return err
	}
	if err := config.ValidateCommandName(name); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if len(code) == 0 || len(code) > MaxCodeBytes {
		return fmt.Errorf("%w: code must be 1-%d bytes", ErrInvalid, MaxCodeBytes)
	}
	if _, inCfg := d.Cfg.Codes[name]; inCfg {
		return ErrReadOnly
	}
	if err := m.store.Set(deviceName, name, code); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	m.refreshDiscovery(d)
	return nil
}

// RenameCommand renames a UI-managed command.
func (m *Manager) RenameCommand(deviceName, from, to string) error {
	d, err := m.device(deviceName)
	if err != nil {
		return err
	}
	if err := config.ValidateCommandName(to); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := m.mutableCommand(d, from); err != nil {
		return err
	}
	if _, inCfg := d.Cfg.Codes[to]; inCfg {
		return fmt.Errorf("%w: %q is defined in the config file", ErrExists, to)
	}
	if err := m.store.Rename(deviceName, from, to); err != nil {
		return mapStoreErr(err)
	}
	m.refreshDiscovery(d)
	return nil
}

// DeleteCommand removes a UI-managed command.
func (m *Manager) DeleteCommand(deviceName, name string) error {
	d, err := m.device(deviceName)
	if err != nil {
		return err
	}
	if err := m.mutableCommand(d, name); err != nil {
		return err
	}
	if err := m.store.Delete(deviceName, name); err != nil {
		return mapStoreErr(err)
	}
	m.refreshDiscovery(d)
	return nil
}

// mutableCommand checks that name exists and is not config-defined.
func (m *Manager) mutableCommand(d *Device, name string) error {
	if _, inCfg := d.Cfg.Codes[name]; inCfg {
		return ErrReadOnly
	}
	if _, ok := m.store.Get(d.Cfg.Name, name); !ok {
		return fmt.Errorf("%w: command %q", ErrNotFound, name)
	}
	return nil
}

func mapStoreErr(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	case errors.Is(err, store.ErrExists):
		return fmt.Errorf("%w: %v", ErrExists, err)
	}
	return err
}
