package router

import (
	"errors"
	"fmt"

	"github.com/acme/obs-notification-router/internal/domain"
)

var ErrRequiredLabels = errors.New("alert requires non-empty team, severity and service labels")

type Policy struct {
	FallbackSlackChannel     string
	FallbackPagerDutyService string
	OpsgenieEnabled          bool
}

type Engine struct{ policy Policy }

func New(policy Policy) *Engine { return &Engine{policy: policy} }

func (e *Engine) Decide(alert domain.Alert, service domain.Service) (domain.Decision, error) {
	team, serviceName, rawSeverity := alert.Labels["team"], alert.Labels["service"], alert.Labels["severity"]
	severity, ok := domain.ParseSeverity(rawSeverity)
	if team == "" || serviceName == "" || !ok {
		return domain.Decision{}, fmt.Errorf("%w: team=%q service=%q severity=%q", ErrRequiredLabels, team, serviceName, rawSeverity)
	}
	fallback := service.Name == ""
	if fallback {
		service = domain.Service{Name: serviceName, Team: team, Owner: team, SlackChannel: e.policy.FallbackSlackChannel, PagerDutyService: e.policy.FallbackPagerDutyService}
	}
	if service.Team != "" && service.Team != team {
		return domain.Decision{}, fmt.Errorf("catalog team %q does not match alert team %q", service.Team, team)
	}
	if service.SlackChannel == "" {
		service.SlackChannel = e.policy.FallbackSlackChannel
		fallback = true
	}
	d := domain.Decision{Team: team, Service: service, Severity: severity, Fallback: fallback}
	d.Destinations = append(d.Destinations, domain.Destination{Strategy: "slack", Target: service.SlackChannel, Stage: "primary"})
	if severity == domain.SeverityPage {
		pd := service.PagerDutyService
		if pd == "" {
			pd = e.policy.FallbackPagerDutyService
			service.PagerDutyService = pd
			d.Service = service
			d.Fallback = true
		}
		d.Destinations = append(d.Destinations, domain.Destination{Strategy: "pagerduty", Target: pd, Stage: "primary"})
	}
	if e.policy.OpsgenieEnabled && service.OpsgenieTeam != "" && severity != domain.SeverityInfo {
		d.Destinations = append(d.Destinations, domain.Destination{Strategy: "opsgenie", Target: service.OpsgenieTeam, Stage: "primary"})
	}
	return d, nil
}

func EscalationDestinations(service domain.Service, fallbackSlack, fallbackPD string) []domain.Destination {
	channel := service.EscalationSlackChannel
	if channel == "" {
		channel = fallbackSlack
	}
	pd := service.EscalationPagerDutyService
	if pd == "" {
		pd = service.PagerDutyService
	}
	if pd == "" {
		pd = fallbackPD
	}
	return []domain.Destination{{Strategy: "slack", Target: channel, Stage: "escalation"}, {Strategy: "pagerduty", Target: pd, Stage: "escalation"}}
}
