package main

import (
	"context"
	"errors"
	_ "expvar"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"broadlink-to-mqtt-gw/api"
	"broadlink-to-mqtt-gw/bridge"
	"broadlink-to-mqtt-gw/config"
	"broadlink-to-mqtt-gw/hadiscovery"
	"broadlink-to-mqtt-gw/store"
	"broadlink-to-mqtt-gw/version"
	"broadlink-to-mqtt-gw/web"
	"github.com/philipparndt/go-logger"
	"github.com/philipparndt/mqtt-gateway/mqtt"
)

// pprofAddr matches the sibling bridges — always :6060, empty disables.
const pprofAddr = ":6060"

// mqttClientIDPrefix is the client-id prefix passed to mqtt-gateway; the
// gateway appends a random suffix to make it unique per connection.
const mqttClientIDPrefix = "bl2mqtt"

func initPprof() {
	if pprofAddr == "" {
		return
	}
	go func() {
		if err := http.ListenAndServe(pprofAddr, nil); err != nil {
			logger.Error("pprof listener failed", "error", err)
		}
	}()
}

func main() {
	logger.Init("info", logger.Logger())
	logger.Info("broadlink-to-mqtt-gw",
		"version", version.Version,
		"commit", version.GitCommit,
		"built", version.BuildTime,
	)

	if len(os.Args) != 2 {
		logger.Error("Expected config file as argument")
		os.Exit(1)
	}
	configFile := os.Args[1]
	logger.Info("Loading config", "file", configFile)

	cfg, err := config.LoadConfig(configFile)
	if err != nil {
		logger.Error("Failed to load config", "error", err)
		os.Exit(1)
	}
	logger.SetLevel(cfg.LogLevel)
	logger.Info("Config loaded", "devices", len(cfg.Devices), "prefix", cfg.MQTT.Topic)

	initPprof()

	// Connect MQTT — this blocks until the initial connection succeeds.
	// mqtt-gateway installs the LWT ({prefix}/bridge/state -> "offline") from
	// the config Topic and publishes the corresponding "online" on connect.
	mqtt.Start(cfg.MQTT.ToGatewayConfig(), mqttClientIDPrefix)

	mqttAdapter := &bridge.GatewayMQTT{Retain: cfg.MQTT.Retain}
	discovery := hadiscovery.NewPublisher(cfg.MQTT.Topic, func(topic string, payload []byte, retain bool) {
		mqttAdapter.Publish(topic, payload, retain)
	})

	cmdStore, err := store.Open(cfg.DataFile)
	if err != nil {
		logger.Error("Failed to open data file", "file", cfg.DataFile, "error", err)
		os.Exit(1)
	}
	logger.Info("Command store", "file", cfg.DataFile)

	mgr := bridge.NewManager(cfg, mqttAdapter, discovery, cmdStore)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go mgr.Run(ctx)

	var uiServer *http.Server
	if cfg.UI.Enabled {
		uiServer = &http.Server{
			Addr:              cfg.UI.Listen,
			Handler:           api.New(mgr, web.FS()),
			ReadHeaderTimeout: 10 * time.Second,
		}
		go func() {
			logger.Info("Web UI listening", "addr", cfg.UI.Listen)
			if err := uiServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("Web UI listener failed", "error", err)
			}
		}()
	}
	logger.Info("Application is now ready")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	logger.Info("Shutting down")

	// Stop the device loops first, then clear HA discovery topics before the
	// MQTT client disconnects (the LWT then publishes bridge/state -> offline).
	cancel()
	if uiServer != nil {
		shutdownCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
		_ = uiServer.Shutdown(shutdownCtx)
		stop()
	}
	discovery.Cleanup()
}
