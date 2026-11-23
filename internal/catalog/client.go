package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/acme/obs-notification-router/internal/domain"
)

var ErrNotFound = errors.New("service not found in catalog")

type Reader interface {
	Service(ctx context.Context, environment, name string) (domain.Service, error)
	TeamsForSlackUser(context.Context, string) ([]string, error)
}

type cacheEntry struct {
	value   domain.Service
	expires time.Time
}
type Client struct {
	baseURL, token string
	http           *http.Client
	ttl            time.Duration
	mu             sync.RWMutex
	cache          map[string]cacheEntry
}

func New(client *http.Client, baseURL, token string, ttl time.Duration) *Client {
	return &Client{http: client, baseURL: strings.TrimRight(baseURL, "/"), token: token, ttl: ttl, cache: map[string]cacheEntry{}}
}

// catalogRecord mirrors obs-onboarding-controller's
// internal/catalogapi.ServiceRecord wire format exactly: GET
// {baseURL}/v1/services/{environment}/{name} returns this shape directly
// (no "service" envelope key), with camelCase field names -- not the
// snake_case domain.Service this client used to (incorrectly) decode
// straight into. See obs-developer-portal's backend/internal/upstream/client.go
// for the identical fix applied to that repo's catalog client.
type catalogRecord struct {
	Name             string `json:"name"`
	Environment      string `json:"environment"`
	Team             string `json:"team"`
	ServiceType      string `json:"serviceType"`
	DashboardURL     string `json:"dashboardUrl"`
	SlackChannel     string `json:"slackChannel"`
	PagerDutyService string `json:"pagerDutyService"`
	Status           string `json:"status"`
}

// Service looks up one catalog record. environment is required because
// the catalog keys records by {environment}/{name} -- the same service
// name can exist independently in dev/staging/prod (see catalogRecord's
// source type's own doc comment in obs-onboarding-controller).
func (c *Client) Service(ctx context.Context, environment, name string) (domain.Service, error) {
	cacheKey := environment + "/" + name
	c.mu.RLock()
	cached, ok := c.cache[cacheKey]
	c.mu.RUnlock()
	if ok && time.Now().Before(cached.expires) {
		return cached.value, nil
	}
	var record catalogRecord
	if err := c.get(ctx, "/v1/services/"+url.PathEscape(environment)+"/"+url.PathEscape(name), &record); err != nil {
		return domain.Service{}, err
	}
	if record.Name == "" || record.Status == "decommissioned" {
		return domain.Service{}, ErrNotFound
	}
	service := domain.Service{
		Name: record.Name,
		Team: record.Team,
		// The catalog has no separate "owner" concept distinct from
		// team (see obs-manifest-schema's ownership.team) -- fall back
		// to it rather than leaving this blank in notifications.
		Owner:        record.Team,
		SlackChannel: record.SlackChannel,
		// EscalationSlackChannel, EscalationPagerDutyService and
		// OpsgenieTeam have no source anywhere in the platform today
		// (obs-manifest-schema's ownership only carries team/
		// slackChannel/pagerdutyService) -- left zero-value, which
		// internal/router/engine.go's EscalationDestinations already
		// handles by falling back to the primary destinations rather
		// than escalating nowhere.
		PagerDutyService: record.PagerDutyService,
		RunbookURL:       "", // see notify/slack.go and notify/pagerduty.go: the alert's own annotation is authoritative
		RepositoryURL:    "",
	}
	c.mu.Lock()
	c.cache[cacheKey] = cacheEntry{service, time.Now().Add(c.ttl)}
	c.mu.Unlock()
	return service, nil
}
func (c *Client) TeamsForSlackUser(ctx context.Context, user string) ([]string, error) {
	var response struct {
		Teams []string `json:"teams"`
	}
	err := c.get(ctx, "/v1/identity/slack/"+url.PathEscape(user)+"/teams", &response)
	return response.Teams, err
}
func (c *Client) get(ctx context.Context, path string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("catalog returned %d: %s", resp.StatusCode, data)
	}
	return json.Unmarshal(data, target)
}
