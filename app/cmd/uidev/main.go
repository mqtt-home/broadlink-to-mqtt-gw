// Command uidev serves the web UI and API without an MQTT broker, for UI
// development and demos. By default it drives a simulated remote; pass
// -device <ip> to talk to a real Broadlink device instead.
//
//	go run ./cmd/uidev -addr 127.0.0.1:8081
package main

import (
	"context"
	"crypto/rand"
	"flag"
	"log"
	"net/http"
	"time"

	"broadlink-to-mqtt-gw/api"
	"broadlink-to-mqtt-gw/bridge"
	"broadlink-to-mqtt-gw/broadlink"
	"broadlink-to-mqtt-gw/config"
	"broadlink-to-mqtt-gw/hadiscovery"
	"broadlink-to-mqtt-gw/store"
	"broadlink-to-mqtt-gw/web"
)

type noMQTT struct{}

func (noMQTT) Publish(string, []byte, bool)           {}
func (noMQTT) Subscribe(string, func(string, []byte)) {}

// simRemote pretends to be an RM mini 3.
type simRemote struct{}

func (simRemote) Update(context.Context) (broadlink.Info, error) {
	return broadlink.Info{Model: "RM mini 3 (simulated)", MAC: "c8:f7:42:63:19:97"}, nil
}

func (simRemote) SendData(_ context.Context, code []byte) error {
	log.Printf("sim: send %d bytes", len(code))
	return nil
}

func (simRemote) Learn(ctx context.Context, wait time.Duration) ([]byte, error) {
	select {
	case <-time.After(4 * time.Second):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	code := make([]byte, 60)
	_, _ = rand.Read(code)
	code[0] = 0x26
	return code, nil
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8081", "listen address")
	device := flag.String("device", "", "IP of a real Broadlink remote (default: simulated)")
	data := flag.String("data", "", "commands file (default: in memory)")
	flag.Parse()

	cfg := config.Config{
		MQTT:                   config.MQTTConfig{Topic: "broadlink"},
		PollingIntervalSeconds: 5,
		LearnTimeoutSeconds:    30,
		Devices: []config.Device{{
			Host: orDefault(*device, "sim"), Name: "wohnzimmer",
			Codes: map[string][]byte{"tv_power": {0x26, 0, 1, 0, 0xAA}},
		}},
	}
	st := store.NewMemory()
	if *data != "" {
		var err error
		if st, err = store.Open(*data); err != nil {
			log.Fatal(err)
		}
	}
	disc := hadiscovery.NewPublisher("broadlink", func(string, []byte, bool) {})
	m := bridge.NewManager(cfg, noMQTT{}, disc, st)
	if *device == "" {
		m = bridge.NewManagerWith(cfg, noMQTT{}, disc, st, map[string]bridge.Remote{"wohnzimmer": simRemote{}})
	}
	if *device == "" {
		// Simulated LAN: one known remote and two that can be added.
		m.SetDiscoverFunc(func(ctx context.Context, _ broadlink.DiscoverOptions) ([]broadlink.Found, error) {
			select {
			case <-time.After(1200 * time.Millisecond):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return []broadlink.Found{
				{Host: "sim", Info: broadlink.Info{Model: "RM mini 3", MAC: "c8:f7:42:63:19:97", Name: "Wohnzimmer"}, Supported: true},
				{Host: "192.0.2.21", Info: broadlink.Info{Model: "RM4 mini", MAC: "c8:f7:42:83:21:0f", Name: "Schlafzimmer"}, Supported: true},
				{Host: "192.0.2.30", Info: broadlink.Info{Model: "Broadlink 0x5f1a", MAC: "34:ea:34:00:00:01"}},
			}, nil
		})
	}
	go m.Run(context.Background())

	log.Printf("uidev listening on http://%s (ui built: %v)", *addr, web.FS() != nil)
	srv := &http.Server{Addr: *addr, Handler: api.New(m, web.FS()), ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

func orDefault(v, d string) string {
	if v != "" {
		return v
	}
	return d
}
