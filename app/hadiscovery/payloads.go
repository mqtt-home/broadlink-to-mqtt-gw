package hadiscovery

import (
	"encoding/json"
	"fmt"

	"broadlink-to-mqtt-gw/broadlink"
)

// deviceBlock is the shared HA `device` block that groups all button
// entities of one remote under one device card.
type deviceBlock struct {
	Identifiers   []string   `json:"identifiers"`
	Name          string     `json:"name"`
	Manufacturer  string     `json:"manufacturer"`
	Model         string     `json:"model"`
	ConnectionMAC [][]string `json:"connections,omitempty"`
}

func buildDeviceBlock(deviceName string, info broadlink.Info) deviceBlock {
	b := deviceBlock{
		Identifiers:  []string{objectID(deviceName)},
		Name:         deviceName,
		Manufacturer: "Broadlink",
		Model:        info.Model,
	}
	if info.MAC != "" {
		b.ConnectionMAC = [][]string{{"mac", info.MAC}}
	}
	return b
}

// objectID is the HA object-id / identifier for a given device name, namespaced
// with the `broadlink_` prefix so entities never collide with sibling bridges.
func objectID(deviceName string) string {
	return "broadlink_" + deviceName
}

type buttonConfig struct {
	Name                string      `json:"name"`
	UniqueID            string      `json:"unique_id"`
	CommandTopic        string      `json:"command_topic"`
	PayloadPress        string      `json:"payload_press"`
	EntityCategory      string      `json:"entity_category,omitempty"`
	AvailabilityTopic   string      `json:"availability_topic"`
	PayloadAvailable    string      `json:"payload_available"`
	PayloadNotAvailable string      `json:"payload_not_available"`
	Device              deviceBlock `json:"device"`
}

// ButtonConfig returns the retained discovery topic/payload for a button that
// publishes `payload` to `topic` when pressed.
func ButtonConfig(basePrefix, deviceName string, info broadlink.Info, key, label, action, payload, category string) (string, []byte, error) {
	oid := objectID(deviceName)
	cfg := buttonConfig{
		Name:                label,
		UniqueID:            oid + "_" + key,
		CommandTopic:        fmt.Sprintf("%s/%s/%s", basePrefix, deviceName, action),
		PayloadPress:        payload,
		EntityCategory:      category,
		AvailabilityTopic:   fmt.Sprintf("%s/%s/available", basePrefix, deviceName),
		PayloadAvailable:    "online",
		PayloadNotAvailable: "offline",
		Device:              buildDeviceBlock(deviceName, info),
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("homeassistant/button/%s_%s/config", oid, key), b, nil
}
