package bridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"broadlink-to-mqtt-gw/config"
)

// StatePayload is the JSON shape published to `{prefix}/{name}/state`.
type StatePayload struct {
	Locked      bool    `json:"locked"`
	LastCommand *string `json:"last_command"`
	LastSent    *string `json:"last_sent"` // RFC 3339, UTC
}

// SetCommand is a parsed `/set` payload: either a configured command name
// or a raw base64 code, optionally repeated.
type SetCommand struct {
	Name   string // configured command name (empty when Code is used)
	Code   []byte // raw code from the payload (empty when Name is used)
	Repeat int    // >= 1
}

// MaxRepeat caps the repeat count of a single /set message.
const MaxRepeat = 10

// ParseSetCommand interprets the accepted forms on `{prefix}/{name}/set`:
//   - bare command name:        tv_power
//   - JSON string:              "tv_power"
//   - object with command:      {"command": "tv_power", "repeat": 2}
//   - object with raw code:     {"code": "JgBQAAAB...", "repeat": 1}
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
		}
		if err := json.Unmarshal(trimmed, &obj); err != nil {
			return SetCommand{}, fmt.Errorf("invalid JSON: %w", err)
		}
		if (obj.Command == "") == (obj.Code == "") {
			return SetCommand{}, errors.New("object payload needs exactly one of 'command' or 'code'")
		}
		if obj.Repeat < 0 || obj.Repeat > MaxRepeat {
			return SetCommand{}, fmt.Errorf("repeat must be between 1 and %d", MaxRepeat)
		}
		cmd := SetCommand{Name: obj.Command, Repeat: max(obj.Repeat, 1)}
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
