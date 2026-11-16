package bridge

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"broadlink-to-mqtt-gw/broadlink"
	"broadlink-to-mqtt-gw/config"
	"github.com/philipparndt/go-logger"
)

// FoundView is a device seen by the LAN discovery.
type FoundView struct {
	Host      string `json:"host"`
	Model     string `json:"model"`
	Name      string `json:"name,omitempty"` // name stored on the device
	MAC       string `json:"mac"`
	Supported bool   `json:"supported"`       // an IR remote the bridge can drive
	Added     string `json:"added,omitempty"` // bridge device name if already known
}

// SetDiscoverFunc replaces the LAN discovery (demo and tests).
func (m *Manager) SetDiscoverFunc(f func(context.Context, broadlink.DiscoverOptions) ([]broadlink.Found, error)) {
	m.discover = f
}

// Discover searches the LAN for Broadlink devices and marks those that are
// already managed (same host or MAC).
func (m *Manager) Discover(ctx context.Context) ([]FoundView, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	found, err := m.discover(ctx, broadlink.DiscoverOptions{Networks: m.cfg.Discovery.Networks})
	if err != nil {
		return nil, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]FoundView, 0, len(found))
	for _, f := range found {
		v := FoundView{Host: f.Host, Model: f.Info.Model, Name: f.Info.Name, MAC: f.Info.MAC, Supported: f.Supported}
		for name, d := range m.devices {
			d.mu.Lock()
			sameMAC := d.info.MAC != "" && d.info.MAC == f.Info.MAC
			d.mu.Unlock()
			if sameMAC || hostOnly(d.Cfg.Host) == f.Host {
				v.Added = name
				break
			}
		}
		out = append(out, v)
	}
	return out, nil
}

func hostOnly(h string) string {
	if host, _, err := net.SplitHostPort(h); err == nil {
		return host
	}
	return h
}

// AddDevice registers a device found by Discover (or typed in) and starts
// polling it. It is persisted in the data file.
func (m *Manager) AddDevice(name, host string) error {
	host = strings.TrimSpace(host)
	if err := config.ValidateDeviceName(name); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if host == "" || strings.ContainsAny(host, " /#+") {
		return fmt.Errorf("%w: host %q", ErrInvalid, host)
	}

	m.mu.RLock()
	for n, d := range m.devices {
		if n == name {
			m.mu.RUnlock()
			return fmt.Errorf("%w: device %q", ErrExists, name)
		}
		if hostOnly(d.Cfg.Host) == hostOnly(host) {
			m.mu.RUnlock()
			return fmt.Errorf("%w: host %s is already device %q", ErrExists, host, n)
		}
	}
	m.mu.RUnlock()

	if err := m.store.SetDevice(name, host); err != nil {
		return err
	}
	d := m.register(config.Device{Name: name, Host: host}, SourceUI)
	m.mu.Lock()
	if m.runCtx != nil {
		m.start(d)
	}
	m.mu.Unlock()
	logger.Info("Device added", "device", name, "host", host)
	return nil
}

// RemoveDevice stops and forgets a device that was added in the UI, together
// with its commands and its retained MQTT / Home Assistant topics.
func (m *Manager) RemoveDevice(name string) error {
	m.mu.Lock()
	d, ok := m.devices[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: device %q", ErrNotFound, name)
	}
	if d.Source != SourceUI {
		m.mu.Unlock()
		return fmt.Errorf("%w: the device is defined in the config file", ErrReadOnly)
	}
	if d.learning.Load() {
		m.mu.Unlock()
		return ErrBusy
	}
	delete(m.devices, name)
	for i, n := range m.order {
		if n == name {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	m.mu.Unlock()

	if d.cancel != nil {
		d.cancel()
	}
	if err := m.store.DeleteDevice(name); err != nil {
		return mapStoreErr(err)
	}
	if m.discovery != nil {
		m.discovery.Forget(name)
	}
	for _, suffix := range []string{"state", "available"} {
		m.mqtt.Publish(fmt.Sprintf("%s/%s/%s", m.basePrefix, name, suffix), []byte(""), true)
	}
	logger.Info("Device removed", "device", name)
	return nil
}
