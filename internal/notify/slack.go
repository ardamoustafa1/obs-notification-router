package notify

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/acme/obs-notification-router/internal/domain"
)

type Slack struct {
	token, apiURL string
	client        *http.Client
}

func NewSlack(client *http.Client, token, apiURL string) *Slack {
	return &Slack{token: token, apiURL: strings.TrimRight(apiURL, "/"), client: client}
}
func (s *Slack) Name() string { return "slack" }

func (s *Slack) Send(ctx context.Context, n domain.Notification) (domain.DeliveryResult, error) {
	color := map[domain.Severity]string{domain.SeverityInfo: "#3B82F6", domain.SeverityWarning: "#F59E0B", domain.SeverityPage: "#EF4444"}[n.Severity]
	title := fmt.Sprintf("[%s] %s · %s", strings.ToUpper(string(n.Severity)), n.Service.Name, n.Alert.Labels["alertname"])
	if n.Status == "resolved" {
		color, title = "#22C55E", "[RESOLVED] "+n.Service.Name+" · "+n.Alert.Labels["alertname"]
	}
	if n.Stage == "escalation" {
		title = "[ESCALATION] " + title
	}
	description := n.Alert.Annotations["description"]
	if description == "" {
		description = n.Alert.Annotations["summary"]
	}
	deploy := "unknown"
	if !n.Service.LastDeployAt.IsZero() {
		deploy = n.Service.LastDeployAt.UTC().Format("2006-01-02 15:04 UTC")
	}
	blocks := []any{
		map[string]any{"type": "header", "text": map[string]string{"type": "plain_text", "text": title}},
		map[string]any{"type": "section", "text": map[string]string{"type": "mrkdwn", "text": description}, "fields": []map[string]string{
			{"type": "mrkdwn", "text": "*Team*\n" + n.Service.Team}, {"type": "mrkdwn", "text": "*Owner*\n" + n.Service.Owner},
			{"type": "mrkdwn", "text": "*Environment*\n" + n.Alert.Labels["environment"]}, {"type": "mrkdwn", "text": "*Last deploy*\n" + deploy},
		}},
	}
	actions := []map[string]any{}
	// The alert's own runbook_url annotation is the authoritative link
	// (obs-alert-rule-templates sets it per alert type:
	// https://runbooks.acme.com/{serviceType}/{alertname} -- see that
	// repo's README). n.Service.RunbookURL is a generic, service-level
	// fallback the catalog itself never actually populates today; kept
	// only for a future catalog that might.
	runbookURL := n.Alert.Annotations["runbook_url"]
	if runbookURL == "" {
		runbookURL = n.Service.RunbookURL
	}
	if runbookURL != "" {
		actions = append(actions, map[string]any{"type": "button", "text": map[string]string{"type": "plain_text", "text": "Runbook"}, "url": runbookURL})
	}
	if n.Alert.GeneratorURL != "" {
		actions = append(actions, map[string]any{"type": "button", "text": map[string]string{"type": "plain_text", "text": "Source"}, "url": n.Alert.GeneratorURL})
	}
	if n.Severity == domain.SeverityPage && n.Status == "firing" {
		actions = append(actions, map[string]any{"type": "button", "text": map[string]string{"type": "plain_text", "text": "Acknowledge"}, "action_id": "ack_alert", "value": n.EventKey, "style": "primary"})
	}
	if len(actions) > 0 {
		blocks = append(blocks, map[string]any{"type": "actions", "elements": actions})
	}
	payload := map[string]any{"channel": n.Destination, "text": title, "blocks": blocks, "attachments": []map[string]string{{"color": color}}}
	var response struct {
		OK      bool   `json:"ok"`
		Error   string `json:"error,omitempty"`
		TS      string `json:"ts"`
		Channel string `json:"channel"`
	}
	if err := postJSON(ctx, s.client, s.apiURL+"/chat.postMessage", map[string]string{"Authorization": "Bearer " + s.token}, payload, &response); err != nil {
		return domain.DeliveryResult{}, err
	}
	if !response.OK {
		return domain.DeliveryResult{}, fmt.Errorf("slack chat.postMessage: %s", response.Error)
	}
	return domain.DeliveryResult{ExternalID: response.TS, URL: "https://slack.com/archives/" + response.Channel + "/p" + strings.ReplaceAll(response.TS, ".", "")}, nil
}
