package notify_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/acme/obs-notification-router/internal/domain"
	"github.com/acme/obs-notification-router/internal/notify"
)

func testNotification() domain.Notification {
	return domain.Notification{EventKey: "event-123", Status: "firing", Severity: domain.SeverityPage, Stage: "primary", Destination: "#payments-alerts", Service: domain.Service{Name: "payments-api", Team: "payments", Owner: "team-payments", RunbookURL: "https://runbooks.acme.com/payments", LastDeployAt: time.Date(2026, 9, 17, 9, 0, 0, 0, time.UTC)}, Alert: domain.Alert{Labels: map[string]string{"alertname": "HighErrorRate", "environment": "prod"}, Annotations: map[string]string{"summary": "error rate is high", "description": "5xx ratio exceeded 5%"}, GeneratorURL: "https://thanos.acme.com/graph", Fingerprint: "abc"}}
}

func TestSlackStrategyCallsChatPostMessage(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Errorf("path=%s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer xoxb-test" {
			t.Errorf("authorization=%q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"ts":"1720.001","channel":"C123"}`))
	}))
	defer server.Close()
	result, err := notify.NewSlack(server.Client(), "xoxb-test", server.URL).Send(context.Background(), testNotification())
	if err != nil {
		t.Fatal(err)
	}
	if got["channel"] != "#payments-alerts" {
		t.Fatalf("channel=%v", got["channel"])
	}
	if result.ExternalID != "1720.001" {
		t.Fatalf("external ID=%s", result.ExternalID)
	}
}

func TestPagerDutyStrategyTriggerAndResolve(t *testing.T) {
	var payloads []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		payloads = append(payloads, body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","dedup_key":"event-123"}`))
	}))
	defer server.Close()
	strategy := notify.NewPagerDuty(server.Client(), server.URL, map[string]string{"payments-primary": "secret-routing-key"})
	n := testNotification()
	n.Destination = "payments-primary"
	if _, err := strategy.Send(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	n.Status = "resolved"
	if _, err := strategy.Send(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	if payloads[0]["routing_key"] != "secret-routing-key" || payloads[0]["event_action"] != "trigger" {
		t.Fatalf("trigger=%#v", payloads[0])
	}
	if payloads[1]["event_action"] != "resolve" || payloads[1]["dedup_key"] != "event-123" {
		t.Fatalf("resolve=%#v", payloads[1])
	}
}
