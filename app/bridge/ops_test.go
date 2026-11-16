package bridge

import (
	"errors"
	"strings"
	"testing"
)

func TestSaveRenameDeleteAndDiscovery(t *testing.T) {
	m, mq, d := setup(t, &fakeRemote{})
	_ = m.pollOnce(t.Context(), d) // marks the device as discovered

	if err := m.SaveCommand("rm", "vol_up", []byte{7, 7}); err != nil {
		t.Fatal(err)
	}
	if got := mq.on("homeassistant/button/broadlink_rm_vol_up/config"); len(got) != 1 || got[0].payload == "" {
		t.Errorf("discovery not published: %+v", got)
	}
	views := m.ListDevices()[0].Commands
	if len(views) != 3 || views[2].Name != "vol_up" || views[2].Source != SourceUI || views[2].Bytes != 2 {
		t.Errorf("views = %+v", views)
	}
	if views[0].Source != SourceConfig {
		t.Errorf("config command must be marked read-only: %+v", views[0])
	}

	if err := m.RenameCommand("rm", "vol_up", "louder"); err != nil {
		t.Fatal(err)
	}
	if got := mq.on("homeassistant/button/broadlink_rm_vol_up/config"); len(got) != 2 || got[1].payload != "" {
		t.Errorf("old entity not removed: %+v", got)
	}
	if got := mq.on("homeassistant/button/broadlink_rm_louder/config"); len(got) != 1 {
		t.Errorf("new entity missing: %+v", got)
	}
	if err := m.Send("rm", SetCommand{Name: "louder"}); err != nil {
		t.Errorf("send renamed command: %v", err)
	}

	if err := m.DeleteCommand("rm", "louder"); err != nil {
		t.Fatal(err)
	}
	if err := m.Send("rm", SetCommand{Name: "louder"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("send deleted command: %v", err)
	}
}

func TestOpsErrors(t *testing.T) {
	m, _, _ := setup(t, &fakeRemote{})
	check := func(name string, err, want error) {
		t.Helper()
		if !errors.Is(err, want) {
			t.Errorf("%s: got %v, want %v", name, err, want)
		}
	}
	check("unknown device", m.SaveCommand("zz", "a", []byte{1}), ErrNotFound)
	check("bad name", m.SaveCommand("rm", "a b", []byte{1}), ErrInvalid)
	check("reserved", m.SaveCommand("rm", "learn", []byte{1}), ErrInvalid)
	check("empty code", m.SaveCommand("rm", "a", nil), ErrInvalid)
	check("huge code", m.SaveCommand("rm", "a", make([]byte, MaxCodeBytes+1)), ErrInvalid)
	check("overwrite config", m.SaveCommand("rm", "power", []byte{1}), ErrReadOnly)
	check("delete config", m.DeleteCommand("rm", "power"), ErrReadOnly)
	check("rename config", m.RenameCommand("rm", "power", "x"), ErrReadOnly)
	check("delete missing", m.DeleteCommand("rm", "ghost"), ErrNotFound)
	check("rename missing", m.RenameCommand("rm", "ghost", "x"), ErrNotFound)
	check("repeat too high", m.Send("rm", SetCommand{Name: "power", Repeat: MaxRepeat + 1}), ErrInvalid)

	_ = m.SaveCommand("rm", "a", []byte{1})
	_ = m.SaveCommand("rm", "b", []byte{1})
	check("rename onto ui", m.RenameCommand("rm", "a", "b"), ErrExists)
	check("rename onto config", m.RenameCommand("rm", "a", "power"), ErrExists)
	check("rename to bad name", m.RenameCommand("rm", "a", "x/y"), ErrInvalid)
}

func TestLearnStatusViaAPI(t *testing.T) {
	r := &fakeRemote{learnCode: []byte{0x26, 0}}
	m, _, d := setup(t, r)
	if v, _ := m.LearnStatus("rm"); v.State != "idle" {
		t.Errorf("initial = %+v", v)
	}
	if err := m.StartLearn("rm", "tv"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { v, _ := m.LearnStatus("rm"); return v.State == "done" && !d.learning.Load() })
	v, _ := m.LearnStatus("rm")
	if v.Code != "JgA=" || v.Hint != "tv" || v.Error != "" {
		t.Errorf("done = %+v", v)
	}
	if err := m.StartLearn("zz", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown device: %v", err)
	}
}

func TestLearnBusyAndError(t *testing.T) {
	gate := make(chan struct{})
	r := &fakeRemote{learnGate: gate, learnErr: errors.New("no signal")}
	m, _, d := setup(t, r)
	_ = m.StartLearn("rm", "")
	if err := m.StartLearn("rm", ""); !errors.Is(err, ErrBusy) {
		t.Errorf("second start: %v", err)
	}
	if v, _ := m.LearnStatus("rm"); v.State != "learning" || v.Timeout != 5 {
		t.Errorf("status = %+v", v)
	}
	close(gate)
	waitFor(t, func() bool { v, _ := m.LearnStatus("rm"); return v.State == "error" && !d.learning.Load() })
	if v, _ := m.LearnStatus("rm"); !strings.Contains(v.Error, "no signal") {
		t.Errorf("status = %+v", v)
	}
}
