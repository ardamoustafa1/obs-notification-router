package notify

import (
	"context"
	"fmt"
	"net/http"

	"github.com/acme/obs-notification-router/internal/domain"
)

type PagerDuty struct {
	apiURL      string
	routingKeys map[string]string
	client      *http.Client
}

func NewPagerDuty(client *http.Client, apiURL string, routingKeys map[string]string) *PagerDuty {
	return &PagerDuty{client: client, apiURL: apiURL, routingKeys: routingKeys}
}
func (p *PagerDuty) Name() string { return "pagerduty" }
func (p *PagerDuty) Send(ctx context.Context, n domain.Notification) (domain.DeliveryResult, error) {
	key, ok := p.routingKeys[n.Destination]
	if !ok {
		return domain.DeliveryResult{}, fmt.Errorf("no PagerDuty routing key configured for logical service %q", n.Destination)
	}
	action := "trigger"
	if n.Status == "resolved" {
		action = "resolve"
	}
	if n.Stage == "acknowledgement" {
		action = "acknowledge"
	}
	payload := map[string]any{"routing_key": key, "event_action": action, "dedup_key": n.EventKey}
	if n.IncidentKey != "" {
		payload["dedup_key"] = n.IncidentKey
	}
	if action == "trigger" {
		// See slack.go's identical comment: the alert's own runbook_url
		// annotation is authoritative; n.Service.RunbookURL is a
		// fallback the catalog doesn't populate today.
		runbookURL := n.Alert.Annotations["runbook_url"]
		if runbookURL == "" {
			runbookURL = n.Service.RunbookURL
		}
		payload["payload"] = map[string]any{
			"summary": fmt.Sprintf("[%s] %s: %s", n.Severity, n.Service.Name, n.Alert.Annotations["summary"]),
			"source":  n.Service.Name, "severity": "critical", "component": n.Service.Name,
			"group": n.Service.Team, "class": n.Alert.Labels["alertname"], "custom_details": map[string]any{
				"owner": n.Service.Owner, "runbook": runbookURL, "last_deploy": n.Service.LastDeployAt, "labels": n.Alert.Labels,
			},
		}
		payload["links"] = []map[string]string{{"href": runbookURL, "text": "Runbook"}, {"href": n.Alert.GeneratorURL, "text": "Alert source"}}
	}
	var response struct {
		Status   string `json:"status"`
		Message  string `json:"message"`
		DedupKey string `json:"dedup_key"`
	}
	if err := postJSON(ctx, p.client, p.apiURL, nil, payload, &response); err != nil {
		return domain.DeliveryResult{}, err
	}
	return domain.DeliveryResult{ExternalID: n.EventKey}, nil
}
