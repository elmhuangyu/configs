package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/elmhuangyu/configs/pkg/configlib"
)

// AppConfig demonstrates binding of hierarchical configs with configlib:
// - Dynamic prefix mapping (`key:"agents/"`)
// - Namespace prefix mapping (`key:"global/"`)
// - Structured YAML leaf (`key:"manifest.yaml"`)
// - Scalar text leaf (`key:"prompt.txt"`)
type AppConfig struct {
	Agents map[string]AgentConfig `key:"agents/"`
	Global GlobalSettings         `key:"global/"`
}

type AgentConfig struct {
	Manifest AgentManifest `key:"manifest.yaml"`
	Prompt   string        `key:"prompt.txt"`
}

type AgentManifest struct {
	Model       string  `yaml:"model"`
	Temperature float64 `yaml:"temperature"`
	APIKey      string  `yaml:"api_key"`
}

type GlobalSettings struct {
	LogLevel string `key:"log_level"`
	AppEnv   string `key:"app_env"`
}

func main() {
	endpoint := os.Getenv("ETCD_ENDPOINT")
	if endpoint == "" {
		endpoint = "127.0.0.1:2379"
	}
	prefix := os.Getenv("CONFIG_PREFIX")
	if prefix == "" {
		prefix = "/myapp/config"
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
	src.OnError(func(err error) {
		log.Printf("[Source Error] etcd error: %v", err)
	})

	mgr := configlib.NewManager[AppConfig](src)
	mgr.OnChange(func(oldCfg, newCfg *AppConfig) {
		log.Printf("[Config Reloaded] Revision %d", mgr.Revision())
		printConfig("New Config", newCfg)
	})
	mgr.OnError(func(err error) {
		log.Printf("[Config Error] Reload failed: %v", err)
	})

	if err := mgr.Start(ctx); err != nil {
		log.Fatalf("Failed to start ConfigManager: %v", err)
	}

	current := mgr.Get()
	log.Printf("[Config Initialized] Revision %d", mgr.Revision())
	printConfig("Initial Config", current)

	<-ctx.Done()
	log.Println("Shutting down example application...")
}

func printConfig(title string, cfg *AppConfig) {
	if cfg == nil {
		fmt.Printf("--- %s: <nil> ---\n", title)
		return
	}
	fmt.Printf("--- %s ---\n", title)
	fmt.Printf("Global.LogLevel: %s\n", cfg.Global.LogLevel)
	fmt.Printf("Global.AppEnv: %s\n", cfg.Global.AppEnv)
	for name, agent := range cfg.Agents {
		maskedKey := maskSecret(agent.Manifest.APIKey)
		fmt.Printf("Agent [%s]: model=%s, temp=%.2f, api_key=%s, prompt=%s\n",
			name, agent.Manifest.Model, agent.Manifest.Temperature, maskedKey, agent.Prompt)
	}
	fmt.Println("--------------------")
}

func maskSecret(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return s[:2] + "****" + s[len(s)-2:]
}
