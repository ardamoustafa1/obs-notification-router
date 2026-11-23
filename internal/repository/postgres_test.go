package repository_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/acme/obs-notification-router/internal/domain"
	"github.com/acme/obs-notification-router/internal/repository"
)

func TestEscalationStateMachine(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	repo, err := repository.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Pool.Close()
	if err = repo.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = repo.Pool.Exec(ctx, "TRUNCATE audit_log,silences,delivery_rate_limits,notification_outbox,escalations,alert_events RESTART IDENTITY CASCADE")
	if err != nil {
		t.Fatal(err)
	}
	alert := domain.Alert{Status: "firing", Fingerprint: "fsm-1", StartsAt: time.Now().UTC(), Labels: map[string]string{"alertname": "Down", "team": "payments", "service": "payments-api", "severity": "page"}, Annotations: map[string]string{"summary": "down"}}
	svc := domain.Service{Name: "payments-api", Team: "payments", SlackChannel: "#payments", PagerDutyService: "payments-primary"}
	decision := domain.Decision{Team: "payments", Service: svc, Severity: domain.SeverityPage}
	dest := []domain.Destination{{Strategy: "slack", Target: "#payments", Stage: "primary"}, {Strategy: "pagerduty", Target: "payments-primary", Stage: "primary"}}
	inserted, err := repo.StoreAlert(ctx, "fsm-event", "fsm-incident", alert, decision, dest, -time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !inserted {
		t.Fatal("expected inserted event")
	}
	due, err := repo.ClaimAndEnqueueEscalations(ctx, 10, "#platform", "platform-primary")
	if err != nil {
		t.Fatal(err)
	}
	if due != 1 {
		t.Fatalf("due=%d", due)
	}
	ok, err := repo.Acknowledge(ctx, "fsm-event", "tester")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("escalated alert should remain acknowledgeable")
	}
	ok, err = repo.Acknowledge(ctx, "fsm-event", "tester")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("acknowledgement must be idempotently ignored after first transition")
	}
}
