package bridge

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Air-conditioner convenience on top of plain IR commands. Most split units
// send their complete state with every key, so each setpoint is one learned
// command named after its temperature ("16" … "25") plus "aus" for off.
// `/set` then also accepts {"temperature": 21.5} and {"mode": "off|heat|cool|auto"}
// (what a HomeKit thermostat sends).
const (
	// OffCommand is the command name that switches the unit off.
	OffCommand = "aus"
	// defaultTemperature is used when a mode is switched on before any setpoint is known.
	defaultTemperature = 22.0
)

// temperatureCommands returns the commands whose name is a number, keyed by value.
func temperatureCommands(names []string) map[float64]string {
	out := map[float64]string{}
	for _, n := range names {
		if v, err := strconv.ParseFloat(n, 64); err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) {
			out[v] = n
		}
	}
	return out
}

// nearestTemperature picks the available setpoint closest to want (the lower one on a tie).
func nearestTemperature(avail map[float64]string, want float64) (float64, string) {
	vals := make([]float64, 0, len(avail))
	for v := range avail {
		vals = append(vals, v)
	}
	sort.Float64s(vals)
	best := vals[0]
	for _, v := range vals {
		if math.Abs(v-want) < math.Abs(best-want) {
			best = v
		}
	}
	return best, avail[best]
}

// resolveClimate turns a mode/temperature request into a command name. skip is
// true when nothing has to be sent (a new setpoint while the unit is off is only remembered).
func (m *Manager) resolveClimate(d *Device, cmd SetCommand) (name string, skip bool, err error) {
	if cmd.Mode != "" {
		switch strings.ToLower(cmd.Mode) {
		case "off":
			return OffCommand, false, nil
		case "heat", "cool", "auto", "on":
		default:
			return "", false, fmt.Errorf("%w: mode %q (off, heat, cool, auto)", ErrInvalid, cmd.Mode)
		}
	}
	temps := temperatureCommands(m.commandNames(d))
	if len(temps) == 0 {
		return "", false, fmt.Errorf("%w: no temperature commands (16, 17, …) learned", ErrNotFound)
	}
	want := defaultTemperature
	d.mu.Lock()
	if d.targetTemp != nil {
		want = *d.targetTemp
	}
	off := d.off
	d.mu.Unlock()
	if cmd.Temp != nil {
		want = *cmd.Temp
	}
	v, n := nearestTemperature(temps, want)
	if cmd.Temp != nil && off {
		d.mu.Lock()
		d.targetTemp = &v
		d.mu.Unlock()
		return n, true, nil
	}
	return n, false, nil
}

// recordClimate updates the remembered setpoint / off state after a command was sent.
// The caller holds d.mu.
func (d *Device) recordClimate(label string) {
	if label == OffCommand {
		d.off = true
		return
	}
	if v, err := strconv.ParseFloat(label, 64); err == nil {
		d.targetTemp = &v
		d.off = false
	}
}
