package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Address                  string
	DatabaseURL              string
	CatalogURL               string
	CatalogToken             string
	AlertmanagerURL          string
	WebhookToken             string
	APITokens                map[string]string
	SlackBotToken            string
	SlackSigningSecret       string
	SlackAPIURL              string
	PagerDutyAPIURL          string
	PagerDutyRoutingKeys     map[string]string
	OpsgenieEnabled          bool
	OpsgenieAPIURL           string
	OpsgenieAPIKey           string
	FallbackSlackChannel     string
	FallbackPagerDutyService string
	EscalationAfter          time.Duration
	CatalogCacheTTL          time.Duration
	DedupWindow              time.Duration
	RateLimitPerMinute       int
	WorkerPollInterval       time.Duration
	WorkerConcurrency        int
	ShutdownTimeout          time.Duration
}

func Load() (Config, error) {
	c := Config{
		Address:                  env("HTTP_ADDRESS", ":8080"),
		DatabaseURL:              os.Getenv("DATABASE_URL"),
		CatalogURL:               env("CATALOG_URL", "http://obs-onboarding-controller.observability.svc.cluster.local:8080"),
		CatalogToken:             os.Getenv("CATALOG_TOKEN"),
		AlertmanagerURL:          env("ALERTMANAGER_URL", "http://alertmanager-operated.observability.svc.cluster.local:9093"),
		WebhookToken:             os.Getenv("ALERTMANAGER_WEBHOOK_TOKEN"),
		SlackBotToken:            os.Getenv("SLACK_BOT_TOKEN"),
		SlackSigningSecret:       os.Getenv("SLACK_SIGNING_SECRET"),
		SlackAPIURL:              env("SLACK_API_URL", "https://slack.com/api"),
		PagerDutyAPIURL:          env("PAGERDUTY_EVENTS_URL", "https://events.pagerduty.com/v2/enqueue"),
		OpsgenieEnabled:          envBool("OPSGENIE_ENABLED", false),
		OpsgenieAPIURL:           env("OPSGENIE_API_URL", "https://api.opsgenie.com/v2/alerts"),
		OpsgenieAPIKey:           os.Getenv("OPSGENIE_API_KEY"),
		FallbackSlackChannel:     env("FALLBACK_SLACK_CHANNEL", "#platform-alerts"),
		FallbackPagerDutyService: env("FALLBACK_PAGERDUTY_SERVICE", "platform-oncall"),
		EscalationAfter:          envDuration("ESCALATION_AFTER", 5*time.Minute),
		CatalogCacheTTL:          envDuration("CATALOG_CACHE_TTL", time.Minute),
		DedupWindow:              envDuration("DEDUP_WINDOW", 10*time.Minute),
		RateLimitPerMinute:       envInt("RATE_LIMIT_PER_MINUTE", 60),
		WorkerPollInterval:       envDuration("WORKER_POLL_INTERVAL", time.Second),
		WorkerConcurrency:        envInt("WORKER_CONCURRENCY", 4),
		ShutdownTimeout:          envDuration("SHUTDOWN_TIMEOUT", 15*time.Second),
	}
	if err := decodeMap("API_TOKENS_JSON", &c.APITokens); err != nil {
		return Config{}, err
	}
	if err := decodeMap("PAGERDUTY_ROUTING_KEYS_JSON", &c.PagerDutyRoutingKeys); err != nil {
		return Config{}, err
	}
	if c.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if c.WebhookToken == "" {
		return Config{}, errors.New("ALERTMANAGER_WEBHOOK_TOKEN is required")
	}
	if c.SlackBotToken == "" || c.SlackSigningSecret == "" {
		return Config{}, errors.New("SLACK_BOT_TOKEN and SLACK_SIGNING_SECRET are required")
	}
	if len(c.PagerDutyRoutingKeys) == 0 {
		return Config{}, errors.New("PAGERDUTY_ROUTING_KEYS_JSON is required")
	}
	if len(c.APITokens) == 0 {
		return Config{}, errors.New("API_TOKENS_JSON is required")
	}
	if c.OpsgenieEnabled && c.OpsgenieAPIKey == "" {
		return Config{}, errors.New("OPSGENIE_API_KEY is required when OPSGENIE_ENABLED=true")
	}
	return c, nil
}

func decodeMap(name string, target *map[string]string) error {
	value := os.Getenv(name)
	if value == "" {
		*target = map[string]string{}
		return nil
	}
	if err := json.Unmarshal([]byte(value), target); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func envBool(k string, d bool) bool {
	v := os.Getenv(k)
	if v == "" {
		return d
	}
	b, _ := strconv.ParseBool(v)
	return b
}
func envInt(k string, d int) int {
	v := os.Getenv(k)
	if v == "" {
		return d
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return d
	}
	return n
}
func envDuration(k string, d time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return d
	}
	x, err := time.ParseDuration(v)
	if err != nil {
		return d
	}
	return x
}
