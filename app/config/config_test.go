package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func load(t *testing.T, body string) (Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return LoadConfig(p)
}

func TestLoadDefaultsAndCodes(t *testing.T) {
	t.Setenv("MQTT_PASSWORD", "s3")
	c, err := load(t, `{"mqtt":{"url":"tcp://x:1883","password":"${MQTT_PASSWORD}"},
		"devices":[{"host":"10.0.10.211","name":"rm","commands":{"power":"JgA=\n"}}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if c.MQTT.Password != "s3" || !c.MQTT.Retain || c.MQTT.QoS != 1 || c.MQTT.Topic != "broadlink" {
		t.Errorf("defaults wrong: %+v", c.MQTT)
	}
	if c.PollingIntervalSeconds != 30 || c.LearnTimeoutSeconds != 30 || c.LogLevel != "info" {
		t.Errorf("defaults wrong: %+v", c)
	}
	if got := c.Devices[0].Codes["power"]; len(got) != 2 || got[0] != 0x26 {
		t.Errorf("decoded code = %x", got)
	}
}

func TestValidationErrors(t *testing.T) {
	dev := func(extra string) string {
		return `{"mqtt":{"url":"tcp://x"},"devices":[` + extra + `]}`
	}
	cases := map[string]struct{ body, want string }{
		"no url":     {`{"devices":[{"host":"h","name":"n"}]}`, "mqtt.url"},
		"no devices": {`{"mqtt":{"url":"tcp://x"},"devices":[]}`, "devices"},
		"no host":    {dev(`{"name":"n"}`), "host"},
		"bad name":   {dev(`{"host":"h","name":"a/b"}`), "must not contain"},
		"duplicate":  {dev(`{"host":"h","name":"n"},{"host":"h2","name":"n"}`), "duplicate"},
		"bad cmd":    {dev(`{"host":"h","name":"n","commands":{"a b":"JgA="}}`), "may only contain"},
		"reserved":   {dev(`{"host":"h","name":"n","commands":{"learn":"JgA="}}`), "reserved"},
		"bad base64": {dev(`{"host":"h","name":"n","commands":{"a":"!!"}}`), "base64"},
		"empty code": {dev(`{"host":"h","name":"n","commands":{"a":""}}`), "empty"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := load(t, tc.body)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := LoadConfig(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("expected error")
	}
	if _, err := LoadConfig(""); err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestExampleConfigLoads(t *testing.T) {
	c, err := LoadConfig("../../production/config/config-example.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Devices[0].Codes) == 0 {
		t.Error("example has no decoded commands")
	}
}

func TestUIAndDataFileDefaults(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.json")
	body := `{"mqtt":{"url":"tcp://x"},"devices":[{"host":"h","name":"n"}]}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if !c.UI.Enabled || c.UI.Listen != ":8080" {
		t.Errorf("ui defaults wrong: %+v", c.UI)
	}
	if c.DataFile != filepath.Join(dir, "commands.json") {
		t.Errorf("data file = %q", c.DataFile)
	}

	body = `{"mqtt":{"url":"tcp://x"},"data-file":"/data/c.json","ui":{"enabled":false},"devices":[{"host":"h","name":"n"}]}`
	_ = os.WriteFile(p, []byte(body), 0o600)
	c, err = LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.UI.Enabled || c.DataFile != "/data/c.json" {
		t.Errorf("explicit values lost: %+v %q", c.UI, c.DataFile)
	}
}
