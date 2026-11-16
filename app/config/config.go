package config

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/philipparndt/go-logger"
	gwconfig "github.com/philipparndt/mqtt-gateway/config"
)

// commandNameRe restricts command names to something that is safe inside MQTT
// topics, JSON and Home Assistant object ids.
var commandNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ReservedCommands cannot be used as command names because they have a
// meaning on the `/set` topic or in the HA discovery entities.
var ReservedCommands = map[string]struct{}{"learn": {}}

// ValidateCommandName reports whether name can be used as a command name.
func ValidateCommandName(name string) error {
	if !commandNameRe.MatchString(name) {
		return fmt.Errorf("command name %q may only contain letters, digits, '_' and '-'", name)
	}
	if _, reserved := ReservedCommands[name]; reserved {
		return fmt.Errorf("command name %q is reserved", name)
	}
	return nil
}

// ValidateDeviceName reports whether name can be used as a device name (it
// becomes part of the MQTT topics and the Home Assistant object ids).
func ValidateDeviceName(name string) error {
	if !commandNameRe.MatchString(name) {
		return fmt.Errorf("device name %q may only contain letters, digits, '_' and '-'", name)
	}
	if name == "bridge" {
		return fmt.Errorf("device name %q is reserved", name)
	}
	return nil
}

// MQTTConfig holds MQTT broker connection settings.
type MQTTConfig struct {
	URL      string `json:"url"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Retain   bool   `json:"retain"`
	QoS      byte   `json:"qos"`
	Topic    string `json:"topic,omitempty"`
	ClientID string `json:"client-id,omitempty"`
}

// ToGatewayConfig converts to the shared mqtt-gateway config type.
func (m MQTTConfig) ToGatewayConfig() gwconfig.MQTTConfig {
	return gwconfig.MQTTConfig{
		URL:      m.URL,
		Retain:   m.Retain,
		Topic:    m.Topic,
		QoS:      m.QoS,
		Username: m.Username,
		Password: m.Password,
	}
}

// Device is one configured Broadlink remote.
type Device struct {
	Host string `json:"host"`
	Name string `json:"name"`
	// Commands maps a command name to a base64 encoded Broadlink IR code
	// (the format python-broadlink / Home Assistant use).
	Commands map[string]string `json:"commands,omitempty"`

	// Codes holds the decoded Commands; filled in by validation.
	Codes map[string][]byte `json:"-"`
}

// UIConfig controls the embedded web UI / HTTP API.
type UIConfig struct {
	Enabled bool   `json:"enabled"`
	Listen  string `json:"listen,omitempty"`
}

// DiscoveryConfig controls the LAN discovery of Broadlink devices.
type DiscoveryConfig struct {
	// Networks are IPv4 CIDRs (e.g. "10.0.10.0/24") that are probed host by
	// host. Required when the bridge runs in a container, where broadcasts
	// do not reach the LAN.
	Networks []string `json:"networks,omitempty"`
}

// Config is the top-level configuration for the broadlink-to-mqtt gateway.
type Config struct {
	MQTT                   MQTTConfig `json:"mqtt"`
	Devices                []Device   `json:"devices"`
	PollingIntervalSeconds int        `json:"polling-interval-seconds,omitempty"`
	LearnTimeoutSeconds    int        `json:"learn-timeout-seconds,omitempty"`
	// DataFile stores commands managed in the web UI. Defaults to
	// commands.json next to the config file; must be writable.
	DataFile string   `json:"data-file,omitempty"`
	UI       UIConfig `json:"ui"`
	// Discovery configures the "find devices" function of the web UI.
	Discovery DiscoveryConfig `json:"discovery"`
	LogLevel  string          `json:"loglevel,omitempty"`
}

// ApplyDefaults fills in unset optional fields with their documented defaults.
func ApplyDefaults(c *Config) {
	if c.MQTT.QoS == 0 {
		c.MQTT.QoS = 1
	}
	if c.MQTT.Topic == "" {
		c.MQTT.Topic = "broadlink"
	}
	if c.PollingIntervalSeconds == 0 {
		c.PollingIntervalSeconds = 30
	}
	if c.LearnTimeoutSeconds == 0 {
		c.LearnTimeoutSeconds = 30
	}
	if c.UI.Listen == "" {
		c.UI.Listen = ":8080"
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
}

var (
	mu  sync.RWMutex
	cfg Config
)

// LoadConfig reads a JSON config file, substitutes ${ENV} variables,
// applies defaults, validates required fields, and returns the parsed Config.
func LoadConfig(path string) (Config, error) {
	if path == "" {
		return Config{}, errors.New("config path is empty")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}

	data = gwconfig.ReplaceEnvVariables(data)

	// Pre-seed boolean defaults that are true so that an explicit false in the
	// file overrides them correctly after JSON unmarshalling.
	c := Config{MQTT: MQTTConfig{Retain: true}, UI: UIConfig{Enabled: true}}

	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}

	ApplyDefaults(&c)
	if c.DataFile == "" {
		c.DataFile = filepath.Join(filepath.Dir(path), "commands.json")
	}

	if err := validate(&c); err != nil {
		return Config{}, err
	}

	mu.Lock()
	cfg = c
	mu.Unlock()

	logger.Debug("Config loaded", "file", path, "loglevel", c.LogLevel, "devices", len(c.Devices))
	return c, nil
}

// DecodeCode decodes a base64 IR code, tolerating whitespace and missing padding.
func DecodeCode(s string) ([]byte, error) {
	s = strings.Join(strings.Fields(s), "")
	if s == "" {
		return nil, errors.New("empty code")
	}
	s = strings.TrimRight(s, "=")
	b, err := base64.RawStdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid base64: %w", err)
	}
	return b, nil
}

// validate returns a descriptive error when any required field is absent or invalid.
func validate(c *Config) error {
	if c.MQTT.URL == "" {
		return errors.New("required field missing: mqtt.url")
	}
	if len(c.Devices) == 0 {
		return errors.New("required field missing: devices (must contain at least one entry)")
	}

	for _, n := range c.Discovery.Networks {
		if p, err := netip.ParsePrefix(n); err != nil || !p.Addr().Is4() {
			return fmt.Errorf("discovery.networks: %q is not an IPv4 CIDR like 10.0.10.0/24", n)
		}
	}

	seen := make(map[string]struct{}, len(c.Devices))
	for i := range c.Devices {
		d := &c.Devices[i]
		if d.Host == "" {
			return fmt.Errorf("devices[%d]: host is empty", i)
		}
		if d.Name == "" {
			return fmt.Errorf("devices[%d]: name is empty", i)
		}
		if strings.ContainsAny(d.Name, "/#+") {
			return fmt.Errorf("devices[%d]: name %q must not contain '/', '#' or '+'", i, d.Name)
		}
		if _, dup := seen[d.Name]; dup {
			return fmt.Errorf("devices[%d]: duplicate name %q", i, d.Name)
		}
		seen[d.Name] = struct{}{}

		d.Codes = make(map[string][]byte, len(d.Commands))
		for name, raw := range d.Commands {
			if err := ValidateCommandName(name); err != nil {
				return fmt.Errorf("devices[%d] (%s): %w", i, d.Name, err)
			}
			code, err := DecodeCode(raw)
			if err != nil {
				return fmt.Errorf("devices[%d] (%s): command %q: %w", i, d.Name, name, err)
			}
			d.Codes[name] = code
		}
	}
	return nil
}

// Get returns the currently loaded config (zero value if none loaded yet).
func Get() Config {
	mu.RLock()
	defer mu.RUnlock()
	return cfg
}
