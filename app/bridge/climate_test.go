package bridge

import (
	"errors"
	"strings"
	"testing"
)

func climateSetup(t *testing.T) (*Manager, *fakeRemote, *fakeMQTT) {
	t.Helper()
	r := &fakeRemote{}
	m, mq, _ := setup(t, r)
	for i, n := range []string{"aus", "16", "20", "21", "25"} {
		if err := m.SaveCommand("rm", n, []byte{byte(10 + i)}); err != nil {
			t.Fatal(err)
		}
	}
	return m, r, mq
}

func (r *fakeRemote) last() byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.sent) == 0 {
		return 0
	}
	return r.sent[len(r.sent)-1][0]
}

func TestParseClimatePayloads(t *testing.T) {
	c, err := ParseSetCommand([]byte(`{"temperature": 21.5}`))
	if err != nil || c.Temp == nil || *c.Temp != 21.5 {
		t.Fatalf("temperature: %+v %v", c, err)
	}
	c, err = ParseSetCommand([]byte(`{"mode":"off"}`))
	if err != nil || c.Mode != "off" {
		t.Fatalf("mode: %+v %v", c, err)
	}
	for _, bad := range []string{`{"mode":"off","temperature":20}`, `{"temperature":500}`, `{"command":"a","mode":"off"}`} {
		if _, err := ParseSetCommand([]byte(bad)); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestTemperatureSnapsToLearnedSetpoints(t *testing.T) {
	m, r, _ := climateSetup(t)
	cases := []struct {
		want float64
		cmd  byte // code byte of the expected command
	}{{21.4, 13}, {35, 14}, {5, 11}, {18.4, 12}, {18.6, 12}}
	for _, c := range cases {
		w := c.want
		if err := m.Send("rm", SetCommand{Temp: &w}); err != nil {
			t.Fatal(err)
		}
		if r.last() != c.cmd {
			t.Errorf("temperature %v sent code %d, want %d", c.want, r.last(), c.cmd)
		}
	}
}

func TestModeOffAndOn(t *testing.T) {
	m, r, mq := climateSetup(t)
	w := 21.0
	_ = m.Send("rm", SetCommand{Temp: &w})
	if err := m.Send("rm", SetCommand{Mode: "off"}); err != nil || r.last() != 10 {
		t.Fatalf("off: sent %d err %v", r.last(), err)
	}
	if got := mq.on("bl/rm/state"); !strings.Contains(got[len(got)-1].payload, `"mode":"off"`) || !strings.Contains(got[len(got)-1].payload, `"temperature":21`) {
		t.Errorf("state after off: %s", got[len(got)-1].payload)
	}

	// A new setpoint while off is remembered, not sent.
	sent := len(r.sent)
	w = 25
	if err := m.Send("rm", SetCommand{Temp: &w}); err != nil || len(r.sent) != sent {
		t.Fatalf("setpoint while off: sent %d→%d err %v", sent, len(r.sent), err)
	}
	// Switching on sends the remembered setpoint.
	if err := m.Send("rm", SetCommand{Mode: "cool"}); err != nil || r.last() != 14 {
		t.Fatalf("on: sent %d err %v", r.last(), err)
	}
	if got := mq.on("bl/rm/state"); strings.Contains(got[len(got)-1].payload, `"mode"`) {
		t.Errorf("mode must disappear when on: %s", got[len(got)-1].payload)
	}
}

func TestClimateErrors(t *testing.T) {
	m, _, _ := setup(t, &fakeRemote{})
	w := 21.0
	if err := m.Send("rm", SetCommand{Temp: &w}); !errors.Is(err, ErrNotFound) {
		t.Errorf("no setpoints learned: %v", err)
	}
	if err := m.Send("rm", SetCommand{Mode: "off"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("no off command learned: %v", err)
	}
	m2, _, _ := climateSetup(t)
	if err := m2.Send("rm", SetCommand{Mode: "dry"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown mode: %v", err)
	}
}
