package bridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"broadlink-to-mqtt-gw/config"
)

// StatePayload is the JSON shape published to `{prefix}/{name}/state`.
type StatePayload struct {
	Locked      bool    `json:"locked"`
	LastCommand *string `json:"last_command"`
	LastSent    *string `json:"last_sent"` // RFC 3339, UTC

	// Air-conditioner view derived from the commands "aus" and "16" … "25" (see climate.go).
	Mode        string   `json:"mode,omitempty"` // "off" while the last command was "aus"
	Temperature *float64 `json:"temperature"`    // last known setpoint
}

// SetCommand is a parsed `/set` payload: either a configured command name
// or a raw base64 code, optionally repeated.
type SetCommand struct {
	Name   string // configured command name (empty when Code is used)
	Code   []byte // raw code from the payload (empty when Name is used)
	Repeat int    // >= 1

	Mode string   // "off", "heat", "cool" or "auto" (climate request)
	Temp *float64 // setpoint in °C (climate request)
}

// MaxRepeat caps the repeat count of a single /set message.
const MaxRepeat = 10

// ParseSetCommand interprets the accepted forms on `{prefix}/{name}/set`:
//   - bare command name:        tv_power
//   - JSON string:              "tv_power"
//   - object with command:      {"command": "tv_power", "repeat": 2}
//   - object with raw code:     {"code": "JgBQAAAB...", "repeat": 1}
//   - climate:                  {"mode": "off"} or {"temperature": 21}  (see climate.go)
func ParseSetCommand(payload []byte) (SetCommand, error) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return SetCommand{}, errors.New("empty payload")
	}

	if trimmed[0] == '{' {
		var obj struct {
			Command string `json:"command"`
			Code    string `json:"code"`
			Repeat  int    `json:"repeat"`

			Mode        string   `json:"mode"`
			Temperature *float64 `json:"temperature"`
		}
		if err := json.Unmarshal(trimmed, &obj); err != nil {
			return SetCommand{}, fmt.Errorf("invalid JSON: %w", err)
		}
		n := 0
		for _, set := range []bool{obj.Command != "", obj.Code != "", obj.Mode != "", obj.Temperature != nil} {
			if set {
				n++
			}
		}
		if n != 1 {
			return SetCommand{}, errors.New("object payload needs exactly one of 'command', 'code', 'mode' or 'temperature'")
		}
		if obj.Temperature != nil && (math.IsNaN(*obj.Temperature) || *obj.Temperature < -50 || *obj.Temperature > 100) {
			return SetCommand{}, errors.New("temperature out of range")
		}
		if obj.Repeat < 0 || obj.Repeat > MaxRepeat {
			return SetCommand{}, fmt.Errorf("repeat must be between 1 and %d", MaxRepeat)
		}
		cmd := SetCommand{Name: obj.Command, Repeat: max(obj.Repeat, 1), Mode: obj.Mode, Temp: obj.Temperature}
		if obj.Code != "" {
			code, err := config.DecodeCode(obj.Code)
			if err != nil {
				return SetCommand{}, err
			}
			cmd.Code = code
		}
		return cmd, nil
	}

	name := trimmed
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return SetCommand{}, fmt.Errorf("invalid JSON string: %w", err)
		}
		name = []byte(s)
	}
	s := strings.TrimSpace(string(name))
	if s == "" {
		return SetCommand{}, errors.New("empty command name")
	}
	return SetCommand{Name: s, Repeat: 1}, nil
}
