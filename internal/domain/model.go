package domain

import "time"

type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityPage    Severity = "page"
)

func ParseSeverity(value string) (Severity, bool) {
	switch value {
	case "info":
		return SeverityInfo, true
	case "warning", "warn":
		return SeverityWarning, true
	case "page", "critical":
		return SeverityPage, true
	default:
		return "", false
	}
}

type Alert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
}

type AlertmanagerWebhook struct {
	Version            string            `json:"version"`
	GroupKey           string            `json:"groupKey"`
	Status             string            `json:"status"`
	Receiver           string            `json:"receiver"`
	GroupLabels        map[string]string `json:"groupLabels"`
	CommonLabels       map[string]string `json:"commonLabels"`
	CommonAnnotations  map[string]string `json:"commonAnnotations"`
	ExternalURL        string            `json:"externalURL"`
	NotificationReason string            `json:"notification_reason,omitempty"`
	TruncatedAlerts    int               `json:"truncatedAlerts,omitempty"`
	Alerts             []Alert           `json:"alerts"`
}

type Service struct {
	Name                       string    `json:"name"`
	Team                       string    `json:"team"`
	Owner                      string    `json:"owner"`
	SlackChannel               string    `json:"slack_channel"`
	EscalationSlackChannel     string    `json:"escalation_slack_channel"`
	PagerDutyService           string    `json:"pagerduty_service"`
	EscalationPagerDutyService string    `json:"escalation_pagerduty_service"`
	OpsgenieTeam               string    `json:"opsgenie_team"`
	RunbookURL                 string    `json:"runbook_url"`
	RepositoryURL              string    `json:"repository_url"`
	LastDeployAt               time.Time `json:"last_deploy_at"`
}

type Destination struct {
	Strategy string `json:"strategy"`
	Target   string `json:"target"`
	Stage    string `json:"stage"`
}

type Decision struct {
	Team         string        `json:"team"`
	Service      Service       `json:"service"`
	Severity     Severity      `json:"severity"`
	Destinations []Destination `json:"destinations"`
	Fallback     bool          `json:"fallback"`
}

type Notification struct {
	EventKey    string            `json:"event_key"`
	IncidentKey string            `json:"incident_key"`
	Status      string            `json:"status"`
	Severity    Severity          `json:"severity"`
	Stage       string            `json:"stage"`
	Destination string            `json:"destination"`
	Service     Service           `json:"service"`
	Alert       Alert             `json:"alert"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
}

type DeliveryResult struct {
	ExternalID string
	URL        string
}

type MuteRequest struct {
	Service   string        `json:"service"`
	Duration  time.Duration `json:"-"`
	For       string        `json:"for"`
	Reason    string        `json:"reason"`
	CreatedBy string        `json:"created_by"`
	Team      string        `json:"team"`
}
