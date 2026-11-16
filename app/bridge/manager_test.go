package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"broadlink-to-mqtt-gw/broadlink"
	"broadlink-to-mqtt-gw/config"
	"broadlink-to-mqtt-gw/hadiscovery"
)

type pub struct {
	topic   string
	payload string
	retain  bool
}

type fakeMQTT struct {
	mu   sync.Mutex
	pubs []pub
}

func (f *fakeMQTT) Publish(topic string, payload []byte, retain bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pubs = append(f.pubs, pub{topic, string(payload), retain})
}
func (f *fakeMQTT) Subscribe(string, func(string, []byte)) {}
func (f *fakeMQTT) on(topic string) []pub {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []pub
	for _, p := range f.pubs {
		if p.topic == topic {
			out = append(out, p)
		}
	}
	return out
}

type fakeRemote struct {
	mu        sync.Mutex
	sent      [][]byte
	updateErr error
	sendErr   error
	learnCode []byte
	learnErr  error
	learnGate chan struct{} // when set, Learn blocks until closed
}

func (r *fakeRemote) Update(context.Context) (broadlink.Info, error) {
	return broadlink.Info{Model: "RM mini 3", MAC: "aa:bb"}, r.updateErr
}
func (r *fakeRemote) SendData(_ context.Context, code []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sendErr != nil {
		return r.sendErr
	}
	r.sent = append(r.sent, code)
	return nil
}
func (r *fakeRemote) Learn(context.Context, time.Duration) ([]byte, error) {
	if r.learnGate != nil {
		<-r.learnGate
	}
	return r.learnCode, r.learnErr
}

func setup(t *testing.T, r *fakeRemote) (*Manager, *fakeMQTT, *Device) {
	t.Helper()
	cfg := config.Config{
		MQTT:                   config.MQTTConfig{Topic: "bl", Retain: true},
		PollingIntervalSeconds: 30,
		LearnTimeoutSeconds:    5,
		Devices: []config.Device{{
			Host: "h", Name: "rm",
			Codes: map[string][]byte{"power": {1, 2}, "mute": {3}},
		}},
	}
	mq := &fakeMQTT{}
	disc := hadiscovery.NewPublisher("bl", func(topic string, p []byte, retain bool) { mq.Publish(topic, p, retain) })
	m := NewManagerWith(cfg, mq, disc, nil, map[string]Remote{"rm": r})
	return m, mq, m.devices["rm"]
}

func TestPollPublishesAvailabilityStateAndDiscovery(t *testing.T) {
	r := &fakeRemote{}
	m, mq, d := setup(t, r)
	if err := m.pollOnce(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	m.publishAvailability(d, true)
	m.publishAvailability(d, true) // deduplicated

	if got := mq.on("bl/rm/available"); len(got) != 1 || got[0].payload != "online" {
		t.Errorf("availability = %+v", got)
	}
	if got := mq.on("bl/rm/state"); len(got) != 1 || got[0].payload != `{"locked":false,"last_command":null,"last_sent":null,"temperature":null}` {
		t.Errorf("state = %+v", got)
	}
	for _, topic := range []string{
		"homeassistant/button/broadlink_rm_power/config",
		"homeassistant/button/broadlink_rm_mute/config",
		"homeassistant/button/broadlink_rm_learn/config",
	} {
		got := mq.on(topic)
		if len(got) != 1 || !got[0].retain {
			t.Errorf("%s: %+v", topic, got)
		}
	}
	// Second poll: no new discovery, no duplicate state.
	_ = m.pollOnce(context.Background(), d)
	if len(mq.on("bl/rm/state")) != 1 || len(mq.on("homeassistant/button/broadlink_rm_power/config")) != 1 {
		t.Error("second poll republished")
	}
}

func TestPollErrorIsReturned(t *testing.T) {
	r := &fakeRemote{updateErr: errors.New("boom")}
	m, _, d := setup(t, r)
	if err := m.pollOnce(context.Background(), d); err == nil {
		t.Fatal("expected error")
	}
}

func TestSetSendsCodeAndPublishesState(t *testing.T) {
	r := &fakeRemote{}
	m, mq, d := setup(t, r)
	m.handleMessage("bl/rm/set", []byte(`{"command":"power","repeat":2}`))
	if len(r.sent) != 2 || r.sent[0][0] != 1 {
		t.Fatalf("sent = %v", r.sent)
	}
	st := mq.on("bl/rm/state")
	if len(st) != 1 || !strings.Contains(st[0].payload, `"last_command":"power"`) {
		t.Errorf("state = %+v", st)
	}
	_ = d
}

func TestSetRawCodeAndGet(t *testing.T) {
	r := &fakeRemote{}
	m, mq, _ := setup(t, r)
	m.handleMessage("bl/rm/get", nil) // nothing cached yet
	if len(mq.on("bl/rm/state")) != 0 {
		t.Error("get published without cache")
	}
	m.handleMessage("bl/rm/set", []byte(`{"code":"JgA="}`))
	if len(r.sent) != 1 || len(r.sent[0]) != 2 {
		t.Fatalf("sent = %v", r.sent)
	}
	m.handleMessage("bl/rm/get", nil)
	if len(mq.on("bl/rm/state")) != 2 {
		t.Error("get did not republish cached state")
	}
}

func TestSetRejections(t *testing.T) {
	r := &fakeRemote{}
	m, mq, _ := setup(t, r)
	m.handleMessage("bl/rm/set", []byte("unknown"))
	m.handleMessage("bl/rm/set", []byte(""))
	m.handleMessage("bl/nope/set", []byte("power"))
	r.sendErr = errors.New("udp down")
	m.handleMessage("bl/rm/set", []byte("power"))
	if len(r.sent) != 0 || len(mq.on("bl/rm/state")) != 0 {
		t.Errorf("unexpected effects: sent=%v pubs=%v", r.sent, mq.pubs)
	}
}

func TestLearnPublishesCode(t *testing.T) {
	r := &fakeRemote{learnCode: []byte{0x26, 0x00}}
	m, mq, d := setup(t, r)
	m.handleMessage("bl/rm/learn", []byte("tv_power"))
	waitFor(t, func() bool { return len(mq.on("bl/rm/learned")) == 1 && !d.learning.Load() })

	var got map[string]string
	if err := json.Unmarshal([]byte(mq.on("bl/rm/learned")[0].payload), &got); err != nil {
		t.Fatal(err)
	}
	if got["code"] != "JgA=" || got["command"] != "tv_power" || got["learned_at"] == "" {
		t.Errorf("learned = %v", got)
	}
}

func TestLearnErrorAndConcurrency(t *testing.T) {
	gate := make(chan struct{})
	r := &fakeRemote{learnGate: gate, learnErr: errors.New("learn timeout")}
	m, mq, d := setup(t, r)

	m.handleMessage("bl/rm/learn", []byte("learn"))
	waitFor(t, d.learning.Load)
	m.handleMessage("bl/rm/learn", []byte("learn")) // second one is ignored
	m.handleMessage("bl/rm/set", []byte("power"))   // blocked while learning
	if len(r.sent) != 0 {
		t.Error("set was sent during learning")
	}
	if err := m.pollOnce(context.Background(), d); err != nil || len(mq.on("bl/rm/state")) != 0 {
		t.Error("poll must be a no-op while learning")
	}
	close(gate)
	waitFor(t, func() bool { return len(mq.on("bl/rm/learned")) == 1 && !d.learning.Load() })
	p := mq.on("bl/rm/learned")[0]
	if p.retain || !strings.Contains(p.payload, "learn timeout") {
		t.Errorf("error publish = %+v", p)
	}
}

func TestCleanupClearsDiscovery(t *testing.T) {
	m, mq, d := setup(t, &fakeRemote{})
	_ = m.pollOnce(context.Background(), d)
	m.discovery.Cleanup()
	if got := mq.on("homeassistant/button/broadlink_rm_power/config"); len(got) != 2 || got[1].payload != "" {
		t.Errorf("cleanup = %+v", got)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}
