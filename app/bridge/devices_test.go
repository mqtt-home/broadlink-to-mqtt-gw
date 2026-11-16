package bridge

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"broadlink-to-mqtt-gw/broadlink"
	"broadlink-to-mqtt-gw/config"
	"broadlink-to-mqtt-gw/store"
)

func newDeviceTestManager(t *testing.T) (*Manager, *fakeMQTT, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "commands.json")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		MQTT:    config.MQTTConfig{Topic: "broadlink"},
		Devices: []config.Device{{Name: "rm", Host: "10.0.0.1"}},
	}
	config.ApplyDefaults(&cfg)
	mq := &fakeMQTT{}
	return NewManager(cfg, mq, nil, st), mq, path
}

func TestAddRemoveDevice(t *testing.T) {
	m, mq, path := newDeviceTestManager(t)

	if err := m.AddDevice("kind2", " 10.0.0.2 "); err != nil {
		t.Fatal(err)
	}
	var added *DeviceView
	for _, v := range m.ListDevices() {
		if v.Name == "kind2" {
			added = &v
		}
	}
	if added == nil || added.Host != "10.0.0.2" || added.Source != SourceUI {
		t.Fatalf("added device = %+v", added)
	}

	// Survives a restart.
	st2, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	m2 := NewManager(m.cfg, mq, nil, st2)
	if len(m2.ListDevices()) != 2 {
		t.Fatalf("after reload: %+v", m2.ListDevices())
	}

	if err := m.SaveCommand("kind2", "an", []byte{1, 2}); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveDevice("kind2"); err != nil {
		t.Fatal(err)
	}
	if len(m.ListDevices()) != 1 {
		t.Errorf("device still listed: %+v", m.ListDevices())
	}
	if got := mq.on("broadlink/kind2/available"); len(got) != 1 || got[0].payload != "" || !got[0].retain {
		t.Errorf("retained availability not cleared: %+v", got)
	}
	if _, ok := m.store.Get("kind2", "an"); ok {
		t.Error("commands of removed device kept")
	}
	if len(m.store.Devices()) != 0 {
		t.Error("device kept in store")
	}
}

func TestAddDeviceRejects(t *testing.T) {
	m, _, _ := newDeviceTestManager(t)
	cases := []struct {
		name, host string
		want       error
	}{
		{"rm", "10.0.0.9", ErrExists},       // name taken
		{"other", "10.0.0.1", ErrExists},    // host taken
		{"other", "10.0.0.1:80", ErrExists}, // host taken, with port
		{"a/b", "10.0.0.9", ErrInvalid},     // bad name
		{"bridge", "10.0.0.9", ErrInvalid},  // reserved
		{"ok", "", ErrInvalid},              // no host
		{"ok", "10.0.0.9 x", ErrInvalid},    // bad host
	}
	for _, c := range cases {
		if err := m.AddDevice(c.name, c.host); !errors.Is(err, c.want) {
			t.Errorf("AddDevice(%q, %q) = %v, want %v", c.name, c.host, err, c.want)
		}
	}
}

func TestRemoveDeviceErrors(t *testing.T) {
	m, _, _ := newDeviceTestManager(t)
	if err := m.RemoveDevice("rm"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("config device: %v", err)
	}
	if err := m.RemoveDevice("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown device: %v", err)
	}
}

func TestAddedDeviceStartsWhileRunning(t *testing.T) {
	m, mq, _ := newDeviceTestManager(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	// The new device has no reachable host, so the loop reports it offline.
	if err := m.AddDevice("kind2", "127.0.0.1:9"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(mq.on("broadlink/kind2/available")) > 0 })
}

func TestDiscoverMarksKnownDevices(t *testing.T) {
	m, _, _ := newDeviceTestManager(t)
	m.discover = func(context.Context, broadlink.DiscoverOptions) ([]broadlink.Found, error) {
		return []broadlink.Found{
			{Host: "10.0.0.1", Info: broadlink.Info{Model: "RM mini 3", MAC: "aa:aa"}, Supported: true},
			{Host: "10.0.0.7", Info: broadlink.Info{Model: "RM mini 3", MAC: "bb:bb", Name: "Wohnzimmer"}, Supported: true},
			{Host: "10.0.0.8", Info: broadlink.Info{Model: "Broadlink 0x1234", MAC: "cc:cc"}},
		}, nil
	}
	got, err := m.Discover(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Added != "rm" || got[1].Added != "" || got[2].Supported {
		t.Fatalf("got %+v", got)
	}
}
