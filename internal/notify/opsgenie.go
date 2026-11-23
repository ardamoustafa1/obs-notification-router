package notify

import (
	"context"
	"net/http"

	"github.com/acme/obs-notification-router/internal/domain"
)

// Opsgenie is isolated behind Strategy because Atlassian is retiring standalone
// Opsgenie. Existing installations can enable this adapter without coupling the router.
type Opsgenie struct {
	apiURL, apiKey string
	client         *http.Client
}

func NewOpsgenie(client *http.Client, apiURL, apiKey string) *Opsgenie {
	return &Opsgenie{client: client, apiURL: apiURL, apiKey: apiKey}
}
func (o *Opsgenie) Name() string { return "opsgenie" }
func (o *Opsgenie) Send(ctx context.Context, n domain.Notification) (domain.DeliveryResult, error) {
	actionURL := o.apiURL
	methodPayload := map[string]any{"message": n.Alert.Annotations["summary"], "alias": n.EventKey, "description": n.Alert.Annotations["description"], "teams": []map[string]string{{"name": n.Destination}}, "tags": []string{n.Service.Name, string(n.Severity)}, "details": n.Alert.Labels, "priority": "P2"}
	if n.Severity == domain.SeverityPage {
		methodPayload["priority"] = "P1"
	}
	if n.Status == "resolved" {
		actionURL += "/" + n.EventKey + "/close?identifierType=alias"
		methodPayload = map[string]any{"note": "Resolved by Alertmanager"}
	}
	var response struct {
		RequestID string `json:"requestId"`
	}
	err := postJSON(ctx, o.client, actionURL, map[string]string{"Authorization": "GenieKey " + o.apiKey}, methodPayload, &response)
	return domain.DeliveryResult{ExternalID: response.RequestID}, err
}
