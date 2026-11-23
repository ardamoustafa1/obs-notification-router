package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/acme/obs-notification-router/internal/catalog"
	"github.com/acme/obs-notification-router/internal/domain"
	"github.com/acme/obs-notification-router/internal/repository"
	"github.com/acme/obs-notification-router/internal/router"
)

type Processor struct {
	catalog         catalog.Reader
	router          *router.Engine
	repo            *repository.Postgres
	escalationAfter time.Duration
	dedupWindow     time.Duration
	logger          *slog.Logger
}

func NewProcessor(c catalog.Reader, r *router.Engine, repo *repository.Postgres, after, dedupWindow time.Duration, logger *slog.Logger) *Processor {
	return &Processor{catalog: c, router: r, repo: repo, escalationAfter: after, dedupWindow: dedupWindow, logger: logger}
}

type Result struct {
	Accepted   int `json:"accepted"`
	Duplicates int `json:"duplicates"`
	Rejected   int `json:"rejected"`
}

func (p *Processor) Process(ctx context.Context, webhook domain.AlertmanagerWebhook) (Result, error) {
	var result Result
	for _, alert := range webhook.Alerts {
		if alert.Status == "" {
			alert.Status = webhook.Status
		}
		if alert.Status != "firing" && alert.Status != "resolved" {
			result.Rejected++
			continue
		}
		serviceName := alert.Labels["service"]
		environment := alert.Labels["environment"]
		svc, err := p.catalog.Service(ctx, environment, serviceName)
		if err != nil && !errors.Is(err, catalog.ErrNotFound) {
			return result, fmt.Errorf("catalog lookup %s/%s: %w", environment, serviceName, err)
		}
		decision, err := p.router.Decide(alert, svc)
		if err != nil {
			p.logger.Warn("alert rejected", "error", err, "fingerprint", alert.Fingerprint)
			result.Rejected++
			continue
		}
		if alert.Status == "resolved" && decision.Severity == domain.SeverityPage {
			for _, destination := range router.EscalationDestinations(decision.Service, decision.Service.EscalationSlackChannel, decision.Service.PagerDutyService) {
				if destination.Strategy == "pagerduty" && destination.Target != decision.Service.PagerDutyService {
					decision.Destinations = append(decision.Destinations, destination)
				}
			}
		}
		incidentKey := IncidentKey(alert, p.dedupWindow)
		key := eventKey(incidentKey, alert.Status)
		inserted, err := p.repo.StoreAlert(ctx, key, incidentKey, alert, decision, decision.Destinations, p.escalationAfter)
		if err != nil {
			return result, err
		}
		if inserted {
			result.Accepted++
		} else {
			result.Duplicates++
		}
	}
	return result, nil
}
func eventKey(incidentKey, status string) string {
	sum := sha256.Sum256([]byte(incidentKey + "|" + status))
	return hex.EncodeToString(sum[:])
}

func IncidentKey(a domain.Alert, window time.Duration) string {
	if window <= 0 {
		window = 10 * time.Minute
	}
	start := a.StartsAt.UTC().Truncate(window).Format(time.RFC3339Nano)
	if start == "0001-01-01T00:00:00Z" {
		start = a.Labels["alertname"]
	}
	sum := sha256.Sum256([]byte(a.Fingerprint + "|" + start))
	return hex.EncodeToString(sum[:])
}
