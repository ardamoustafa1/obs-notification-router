package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/acme/obs-notification-router/internal/alertmanager"
	"github.com/acme/obs-notification-router/internal/catalog"
	"github.com/acme/obs-notification-router/internal/domain"
	"github.com/acme/obs-notification-router/internal/repository"
	"github.com/acme/obs-notification-router/internal/service"
)

type Server struct {
	processor                 *service.Processor
	catalog                   catalog.Reader
	repo                      *repository.Postgres
	alertmanager              *alertmanager.Client
	webhookToken, slackSecret string
	apiTokens                 map[string]string
	logger                    *slog.Logger
}

func New(processor *service.Processor, catalog catalog.Reader, repo *repository.Postgres, am *alertmanager.Client, webhookToken, slackSecret string, apiTokens map[string]string, logger *slog.Logger) *Server {
	return &Server{processor: processor, catalog: catalog, repo: repo, alertmanager: am, webhookToken: webhookToken, slackSecret: slackSecret, apiTokens: apiTokens, logger: logger}
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("POST /v1/webhooks/alertmanager", s.alertmanagerWebhook)
	mux.HandleFunc("POST /v1/slack/commands", s.slackCommand)
	mux.HandleFunc("POST /v1/slack/interactions", s.slackInteraction)
	mux.HandleFunc("POST /v1/mutes", s.mute)
	mux.HandleFunc("POST /v1/acknowledgements", s.acknowledge)
	return recovery(requestLog(mux, s.logger), s.logger)
}
func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.repo.Ready(ctx); err != nil {
		writeError(w, 503, "not_ready", err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ready"})
}
func (s *Server) alertmanagerWebhook(w http.ResponseWriter, r *http.Request) {
	if !bearerMatches(r, s.webhookToken) {
		writeError(w, 401, "unauthorized", "invalid webhook token")
		return
	}
	var body domain.AlertmanagerWebhook
	if err := decodeJSONLoose(w, r, &body); err != nil {
		return
	}
	if body.Version != "4" {
		writeError(w, 422, "unsupported_version", "Alertmanager webhook version 4 is required")
		return
	}
	result, err := s.processor.Process(r.Context(), body)
	if err != nil {
		writeError(w, 503, "processing_failed", err.Error())
		return
	}
	writeJSON(w, 202, result)
}

type muteBody struct{ Service, For, Reason string }

func (s *Server) mute(w http.ResponseWriter, r *http.Request) {
	team, ok := s.apiTeam(r)
	if !ok {
		writeError(w, 401, "unauthorized", "invalid API token")
		return
	}
	var body struct {
		Service     string `json:"service"`
		Environment string `json:"environment"`
		For         string `json:"for"`
		Reason      string `json:"reason"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		return
	}
	if body.Environment == "" {
		body.Environment = "prod" // catalog keys by {environment}/{name}; prod is the common case when unspecified
	}
	duration, err := parseMuteDuration(body.For)
	if err != nil {
		writeError(w, 422, "invalid_duration", err.Error())
		return
	}
	svc, err := s.catalog.Service(r.Context(), body.Environment, body.Service)
	if err != nil {
		writeError(w, 404, "service_not_found", err.Error())
		return
	}
	if team != "platform" && svc.Team != team {
		writeError(w, 403, "forbidden", "token may only mute its own team's services")
		return
	}
	actor := "api:" + team
	m := domain.MuteRequest{Service: body.Service, Team: svc.Team, CreatedBy: actor, Reason: body.Reason, For: body.For, Duration: duration}
	id, ends, err := s.createMute(r.Context(), m)
	if err != nil {
		writeError(w, 502, "silence_failed", err.Error())
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "service": body.Service, "ends_at": ends})
}
func (s *Server) createMute(ctx context.Context, m domain.MuteRequest) (string, time.Time, error) {
	starts := time.Now().UTC()
	ends := starts.Add(m.Duration)
	if m.Reason == "" {
		m.Reason = "planned maintenance"
	}
	id, err := s.alertmanager.CreateSilence(ctx, alertmanager.Silence{Matchers: []alertmanager.Matcher{{Name: "service", Value: m.Service, IsEqual: true}, {Name: "team", Value: m.Team, IsEqual: true}}, StartsAt: starts, EndsAt: ends, CreatedBy: m.CreatedBy, Comment: m.Reason})
	if err != nil {
		return "", time.Time{}, err
	}
	if err = s.repo.SaveSilence(ctx, id, m, starts, ends); err != nil {
		return "", time.Time{}, err
	}
	return id, ends, nil
}

func (s *Server) slackCommand(w http.ResponseWriter, r *http.Request) {
	raw, ok := s.verifiedSlackBody(w, r)
	if !ok {
		return
	}
	values, err := url.ParseQuery(string(raw))
	if err != nil {
		writeError(w, 400, "invalid_form", err.Error())
		return
	}
	fields := strings.Fields(values.Get("text"))
	if len(fields) != 2 && len(fields) != 3 {
		writeSlack(w, "Usage: `/mute <service> <duration> [environment]` (example: `/mute payments-api 2h staging`; environment defaults to `prod`)")
		return
	}
	duration, err := parseMuteDuration(fields[1])
	if err != nil {
		writeSlack(w, err.Error())
		return
	}
	environment := "prod"
	if len(fields) == 3 {
		environment = fields[2]
	}
	svc, err := s.catalog.Service(r.Context(), environment, fields[0])
	if err != nil {
		writeSlack(w, "Service catalog entry not found.")
		return
	}
	teams, err := s.catalog.TeamsForSlackUser(r.Context(), values.Get("user_id"))
	if err != nil || !slices.Contains(teams, svc.Team) {
		writeSlack(w, "You can only mute services owned by one of your teams.")
		return
	}
	m := domain.MuteRequest{Service: svc.Name, Team: svc.Team, CreatedBy: "slack:" + values.Get("user_id"), Reason: "Slack /mute in " + values.Get("channel_id"), For: fields[1], Duration: duration}
	id, ends, err := s.createMute(r.Context(), m)
	if err != nil {
		s.logger.Error("slash command mute failed", "error", err)
		writeSlack(w, "Mute could not be created; the platform team has been notified.")
		return
	}
	writeSlack(w, fmt.Sprintf("Muted `%s` until %s (silence `%s`).", svc.Name, ends.Format(time.RFC3339), id))
}

func (s *Server) slackInteraction(w http.ResponseWriter, r *http.Request) {
	raw, ok := s.verifiedSlackBody(w, r)
	if !ok {
		return
	}
	values, _ := url.ParseQuery(string(raw))
	var payload struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
		Actions []struct {
			ActionID string `json:"action_id"`
			Value    string `json:"value"`
		} `json:"actions"`
	}
	if err := json.Unmarshal([]byte(values.Get("payload")), &payload); err != nil {
		writeError(w, 400, "invalid_payload", err.Error())
		return
	}
	if len(payload.Actions) != 1 || payload.Actions[0].ActionID != "ack_alert" {
		writeError(w, 422, "unsupported_action", "expected ack_alert")
		return
	}
	eventTeam, err := s.repo.EventTeam(r.Context(), payload.Actions[0].Value)
	if err != nil {
		writeError(w, 404, "alert_not_found", "alert event not found")
		return
	}
	teams, err := s.catalog.TeamsForSlackUser(r.Context(), payload.User.ID)
	if err != nil || !slices.Contains(teams, eventTeam) {
		writeError(w, 403, "forbidden", "only the owning team can acknowledge this alert")
		return
	}
	if err := s.ack(r.Context(), payload.Actions[0].Value, "slack:"+payload.User.ID); err != nil {
		writeError(w, 409, "ack_failed", err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"text": "Alert acknowledged."})
}
func (s *Server) acknowledge(w http.ResponseWriter, r *http.Request) {
	team, ok := s.apiTeam(r)
	if !ok {
		writeError(w, 401, "unauthorized", "invalid API token")
		return
	}
	var body struct {
		EventKey string `json:"event_key"`
		Actor    string `json:"actor"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		return
	}
	if body.Actor == "" {
		body.Actor = "api:" + team
	}
	eventTeam, err := s.repo.EventTeam(r.Context(), body.EventKey)
	if err != nil {
		writeError(w, 404, "alert_not_found", "alert event not found")
		return
	}
	if team != "platform" && team != eventTeam {
		writeError(w, 403, "forbidden", "token may only acknowledge its own team's alerts")
		return
	}
	if err := s.ack(r.Context(), body.EventKey, body.Actor); err != nil {
		writeError(w, 409, "ack_failed", err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "acknowledged"})
}
func (s *Server) ack(ctx context.Context, eventKey, actor string) error {
	ok, err := s.repo.Acknowledge(ctx, eventKey, actor)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("alert is not pending or escalated")
	}
	return s.repo.EnqueueAcknowledgement(ctx, eventKey)
}

func (s *Server) verifiedSlackBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, 400, "invalid_body", err.Error())
		return nil, false
	}
	timestamp := r.Header.Get("X-Slack-Request-Timestamp")
	parsed, err := time.ParseDuration(timestamp + "s")
	if err != nil || time.Since(time.Unix(0, 0).Add(parsed)) > 5*time.Minute || time.Since(time.Unix(0, 0).Add(parsed)) < -time.Minute {
		writeError(w, 401, "invalid_signature", "stale Slack timestamp")
		return nil, false
	}
	base := "v0:" + timestamp + ":" + string(raw)
	mac := hmac.New(sha256.New, []byte(s.slackSecret))
	mac.Write([]byte(base))
	expected := "v0=" + hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(expected), []byte(r.Header.Get("X-Slack-Signature"))) != 1 {
		writeError(w, 401, "invalid_signature", "Slack signature verification failed")
		return nil, false
	}
	return raw, true
}
func (s *Server) apiTeam(r *http.Request) (string, bool) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	for expected, team := range s.apiTokens {
		if subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1 {
			return team, true
		}
	}
	return "", false
}
func parseMuteDuration(v string) (time.Duration, error) {
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, errors.New("duration must look like 30m, 2h, or 24h")
	}
	if d < 5*time.Minute || d > 7*24*time.Hour {
		return 0, errors.New("duration must be between 5m and 168h")
	}
	return d, nil
}
func bearerMatches(r *http.Request, expected string) bool {
	actual := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}
func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		writeError(w, 400, "invalid_json", err.Error())
		return err
	}
	return nil
}
func decodeJSONLoose(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		writeError(w, 400, "invalid_json", err.Error())
		return err
	}
	return nil
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func writeSlack(w http.ResponseWriter, text string) {
	writeJSON(w, 200, map[string]string{"response_type": "ephemeral", "text": text})
}
func requestLog(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		logger.Info("http request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start))
	})
}
func recovery(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error("panic", "value", recovered)
				writeError(w, 500, "internal_error", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
