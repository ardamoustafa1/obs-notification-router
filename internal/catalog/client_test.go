package catalog

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeCatalog mimics obs-onboarding-controller's real catalog server: GET
// /v1/services/{environment}/{name} returns the bare record object, not a
// {"service": {...}} envelope. This test exists because a previous version
// of Client.Service called the wrong URL shape (/v1/services/{name}, one
// segment) and expected the wrong envelope -- it would have 404'd or
// returned ErrNotFound against every real catalog lookup.
func fakeCatalog(t *testing.T, path, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestServiceParsesRealCatalogWireFormat(t *testing.T) {
	server := fakeCatalog(t, "/v1/services/prod/checkout-api", `{
		"name": "checkout-api",
		"environment": "prod",
		"team": "team-checkout",
		"serviceType": "http",
		"dashboardUrl": "https://grafana.acme.internal/d/checkout-api-prod",
		"slackChannel": "#checkout-alerts",
		"pagerDutyService": "checkout-production",
		"status": "active"
	}`)

	client := New(server.Client(), server.URL, "", time.Minute)
	svc, err := client.Service(context.Background(), "prod", "checkout-api")
	if err != nil {
		t.Fatal(err)
	}
	if svc.Team != "team-checkout" || svc.SlackChannel != "#checkout-alerts" || svc.PagerDutyService != "checkout-production" {
		t.Errorf("unexpected mapping: %+v", svc)
	}
}

func TestServiceReturnsNotFoundForDecommissionedRecord(t *testing.T) {
	server := fakeCatalog(t, "/v1/services/prod/retired-svc", `{"name":"retired-svc","environment":"prod","team":"team-x","status":"decommissioned"}`)
	client := New(server.Client(), server.URL, "", time.Minute)

	_, err := client.Service(context.Background(), "prod", "retired-svc")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for a decommissioned record, got %v", err)
	}
}

func TestServiceDistinguishesEnvironmentsForTheSameName(t *testing.T) {
	// Same service name, two environments, two different catalog
	// records -- the whole reason Service takes an environment
	// parameter instead of just a name.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/services/dev/checkout-api", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"checkout-api","environment":"dev","team":"team-checkout","slackChannel":"#checkout-dev-alerts","status":"active"}`))
	})
	mux.HandleFunc("GET /v1/services/prod/checkout-api", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"checkout-api","environment":"prod","team":"team-checkout","slackChannel":"#checkout-alerts","status":"active"}`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := New(server.Client(), server.URL, "", time.Minute)
	dev, err := client.Service(context.Background(), "dev", "checkout-api")
	if err != nil {
		t.Fatal(err)
	}
	prod, err := client.Service(context.Background(), "prod", "checkout-api")
	if err != nil {
		t.Fatal(err)
	}
	if dev.SlackChannel == prod.SlackChannel {
		t.Fatalf("expected different records per environment, got the same SlackChannel for both: %q", dev.SlackChannel)
	}
}
