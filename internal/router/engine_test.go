package router_test

import (
	"testing"

	"github.com/acme/obs-notification-router/internal/domain"
	"github.com/acme/obs-notification-router/internal/router"
)

func TestRoutingEverySeverityAndTeam(t *testing.T) {
	engine := router.New(router.Policy{FallbackSlackChannel: "#platform-alerts", FallbackPagerDutyService: "platform-oncall", OpsgenieEnabled: true})
	teams := []string{"payments", "checkout", "identity"}
	severities := []struct {
		name             string
		wantDestinations int
	}{
		{"info", 1},
		{"warning", 2},
		{"page", 3},
	}
	for _, team := range teams {
		for _, severity := range severities {
			t.Run(team+"/"+severity.name, func(t *testing.T) {
				alert := domain.Alert{Labels: map[string]string{"team": team, "service": team + "-api", "severity": severity.name}}
				svc := domain.Service{Name: team + "-api", Team: team, SlackChannel: "#" + team + "-alerts", PagerDutyService: team + "-primary", OpsgenieTeam: team}
				decision, err := engine.Decide(alert, svc)
				if err != nil {
					t.Fatal(err)
				}
				if got := len(decision.Destinations); got != severity.wantDestinations {
					t.Fatalf("got %d destinations, want %d: %#v", got, severity.wantDestinations, decision.Destinations)
				}
				if decision.Destinations[0].Strategy != "slack" || decision.Destinations[0].Target != "#"+team+"-alerts" {
					t.Fatalf("unexpected Slack route: %#v", decision.Destinations[0])
				}
				if severity.name == "page" && decision.Destinations[1].Strategy != "pagerduty" {
					t.Fatalf("page did not route to PagerDuty: %#v", decision.Destinations)
				}
			})
		}
	}
}

func TestRoutingUsesSafeFallbackForMissingCatalogEntry(t *testing.T) {
	engine := router.New(router.Policy{FallbackSlackChannel: "#platform-alerts", FallbackPagerDutyService: "platform-oncall"})
	decision, err := engine.Decide(domain.Alert{Labels: map[string]string{"team": "new-team", "service": "new-api", "severity": "page"}}, domain.Service{})
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Fallback {
		t.Fatal("expected fallback route")
	}
	if decision.Destinations[0].Target != "#platform-alerts" || decision.Destinations[1].Target != "platform-oncall" {
		t.Fatalf("unsafe fallback: %#v", decision.Destinations)
	}
}

func TestRoutingRejectsInvalidLabelsAndCatalogMismatch(t *testing.T) {
	engine := router.New(router.Policy{})
	cases := []domain.Alert{
		{Labels: map[string]string{"service": "api", "severity": "page"}},
		{Labels: map[string]string{"team": "payments", "severity": "page"}},
		{Labels: map[string]string{"team": "payments", "service": "api", "severity": "urgent"}},
	}
	for _, alert := range cases {
		if _, err := engine.Decide(alert, domain.Service{}); err == nil {
			t.Fatalf("expected rejection for %#v", alert.Labels)
		}
	}
	_, err := engine.Decide(domain.Alert{Labels: map[string]string{"team": "payments", "service": "api", "severity": "page"}}, domain.Service{Name: "api", Team: "identity"})
	if err == nil {
		t.Fatal("expected catalog team mismatch rejection")
	}
}
