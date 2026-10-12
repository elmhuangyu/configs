package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/elmhuangyu/configs/pkg/configlib"
)

// ServiceConfig is the test service configuration model matching e2e scenarios.
type ServiceConfig struct {
	Agents map[string]AgentConfig `key:"agents/" json:"agents"`
	Global GlobalConfig           `key:"global/" json:"global"`
}

type AgentConfig struct {
	Manifest AgentManifest `key:"manifest.yaml" json:"manifest"`
	Prompt   string        `key:"prompt.txt" json:"prompt"`
}

type AgentManifest struct {
	Model       string  `yaml:"model" json:"model"`
	Temperature float64 `yaml:"temperature" json:"temperature"`
	APIKey      string  `yaml:"api_key" json:"api_key"`
}

type GlobalConfig struct {
	LogLevel string `key:"log_level" json:"log_level"`
	AppEnv   string `key:"app_env" json:"app_env"`
}

// ConfigStatus is returned by the test service's HTTP endpoint.
type ConfigStatus struct {
	Revision    int64         `json:"revision"`
	UpdateCount int64         `json:"update_count"`
	Config      ServiceConfig `json:"config"`
}

func main() {
	endpoint := os.Getenv("ETCD_ENDPOINT")
	if endpoint == "" {
		endpoint = "127.0.0.1:2379"
	}
	prefix := os.Getenv("CONFIG_PREFIX")
	if prefix == "" {
		prefix = "/e2e/config"
	}
	httpAddr := os.Getenv("HTTP_ADDR")
	if httpAddr == "" {
		httpAddr = ":8080"
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Printf("Connecting to etcd at %s, prefix %s", endpoint, prefix)
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{endpoint},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		log.Fatalf("Failed to connect to etcd: %v", err)
	}
	defer func() { _ = client.Close() }()

	src := configlib.NewEtcdSource(client, prefix)
	mgr := configlib.NewManager[ServiceConfig](src)

	var updateCount atomic.Int64

	mgr.OnChange(func(oldCfg, newCfg *ServiceConfig) {
		count := updateCount.Add(1)
		log.Printf("[OnChange #%d] Config updated to revision %d", count, mgr.Revision())
	})
	mgr.OnError(func(err error) {
		log.Printf("[OnError] Config update error: %v", err)
	})

	if err := mgr.Start(ctx); err != nil {
		log.Fatalf("Failed to start config manager: %v", err)
	}
	log.Printf("ConfigManager initialized at revision %d", mgr.Revision())

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "OK")
	})
	mux.HandleFunc("/config", func(w http.ResponseWriter, r *http.Request) {
		cfg := mgr.Get()
		status := ConfigStatus{
			Revision:    mgr.Revision(),
			UpdateCount: updateCount.Load(),
		}
		if cfg != nil {
			status.Config = *cfg
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(status)
	})

	server := &http.Server{
		Addr:              httpAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("Test service listening on %s", httpAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("Shutting down test service...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownCtx)
}
