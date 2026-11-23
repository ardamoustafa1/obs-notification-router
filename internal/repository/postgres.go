package repository

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"time"

	"github.com/acme/obs-notification-router/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Postgres struct{ Pool *pgxpool.Pool }

func Open(ctx context.Context, url string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Postgres{Pool: pool}, nil
}

func (p *Postgres) Migrate(ctx context.Context) error {
	conn, err := p.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err = conn.Exec(ctx, `SELECT pg_advisory_lock(670214552001)`); err != nil {
		return err
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(670214552001)`)
	entries, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(entries)
	for _, name := range entries {
		data, err := migrations.ReadFile(name)
		if err != nil {
			return err
		}
		if _, err := conn.Exec(ctx, string(data)); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
	}
	return nil
}

func (p *Postgres) StoreAlert(ctx context.Context, eventKey, incidentKey string, alert domain.Alert, decision domain.Decision, destinations []domain.Destination, escalationAfter time.Duration) (bool, error) {
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	alertJSON, _ := json.Marshal(alert)
	serviceJSON, _ := json.Marshal(decision.Service)
	tag, err := tx.Exec(ctx, `INSERT INTO alert_events(event_key,incident_key,fingerprint,status,team,service,severity,alert,service_metadata)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(event_key) DO NOTHING`, eventKey, incidentKey, alert.Fingerprint, alert.Status, decision.Team, decision.Service.Name, decision.Severity, alertJSON, serviceJSON)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, tx.Commit(ctx)
	}
	for _, d := range destinations {
		n := domain.Notification{EventKey: eventKey, IncidentKey: incidentKey, Status: alert.Status, Severity: decision.Severity, Stage: d.Stage, Destination: d.Target, Service: decision.Service, Alert: alert, Labels: alert.Labels, Annotations: alert.Annotations}
		payload, _ := json.Marshal(n)
		if _, err = tx.Exec(ctx, `INSERT INTO notification_outbox(event_key,strategy,destination,stage,payload) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, eventKey, d.Strategy, d.Target, d.Stage, payload); err != nil {
			return false, err
		}
	}
	if decision.Severity == domain.SeverityPage {
		if alert.Status == "firing" {
			_, err = tx.Exec(ctx, `INSERT INTO escalations(event_key,state,due_at,alert,service_metadata) VALUES($1,'pending',now()+$2::interval,$3,$4) ON CONFLICT(event_key) DO NOTHING`, eventKey, escalationAfter.String(), alertJSON, serviceJSON)
		} else {
			_, err = tx.Exec(ctx, `UPDATE escalations SET state='resolved',resolved_at=now(),updated_at=now() WHERE event_key IN (SELECT event_key FROM alert_events WHERE fingerprint=$1 AND status='firing') AND state IN ('pending','escalated')`, alert.Fingerprint)
		}
		if err != nil {
			return false, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_log(event_type,actor,team,service,alert_fingerprint,outcome,detail) VALUES('alert.received','alertmanager',$1,$2,$3,'accepted',$4)`, decision.Team, decision.Service.Name, alert.Fingerprint, mapJSON(map[string]any{"event_key": eventKey, "fallback": decision.Fallback}))
	if err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

type Job struct {
	ID           int64
	Strategy     string
	Notification domain.Notification
	Attempts     int
}

func (p *Postgres) ClaimJobs(ctx context.Context, limit int) ([]Job, error) {
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id,strategy,payload,attempts FROM notification_outbox WHERE (status='pending' OR (status='processing' AND locked_until<now())) AND next_attempt_at<=now() ORDER BY id FOR UPDATE SKIP LOCKED LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []Job
	for rows.Next() {
		var j Job
		var raw []byte
		if err := rows.Scan(&j.ID, &j.Strategy, &raw, &j.Attempts); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &j.Notification); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	for _, j := range jobs {
		if _, err := tx.Exec(ctx, `UPDATE notification_outbox SET status='processing',locked_until=now()+interval '30 seconds',attempts=attempts+1 WHERE id=$1`, j.ID); err != nil {
			return nil, err
		}
	}
	return jobs, tx.Commit(ctx)
}

func (p *Postgres) CompleteJob(ctx context.Context, j Job, result domain.DeliveryResult) error {
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE notification_outbox SET status='delivered',delivered_at=now(),locked_until=NULL,last_error=NULL WHERE id=$1`, j.ID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_log(event_type,actor,team,service,alert_fingerprint,strategy,destination,outcome,detail) VALUES('notification.delivered','router',$1,$2,$3,$4,$5,'success',$6)`, j.Notification.Service.Team, j.Notification.Service.Name, j.Notification.Alert.Fingerprint, j.Strategy, j.Notification.Destination, mapJSON(map[string]any{"external_id": result.ExternalID, "url": result.URL, "stage": j.Notification.Stage}))
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) FailJob(ctx context.Context, j Job, cause error) error {
	attempt := j.Attempts + 1
	status := "pending"
	if attempt >= 8 {
		status = "failed"
	}
	delay := time.Second * time.Duration(1<<min(attempt, 8))
	if delay > 5*time.Minute {
		delay = 5 * time.Minute
	}
	_, err := p.Pool.Exec(ctx, `UPDATE notification_outbox SET status=$2,next_attempt_at=now()+$3::interval,locked_until=NULL,last_error=$4 WHERE id=$1`, j.ID, status, delay.String(), cause.Error())
	return err
}

func (p *Postgres) AllowDelivery(ctx context.Context, destination string, limit int) (bool, error) {
	var count int
	err := p.Pool.QueryRow(ctx, `INSERT INTO delivery_rate_limits(destination,minute_bucket,count) VALUES($1,date_trunc('minute',now()),1) ON CONFLICT(destination,minute_bucket) DO UPDATE SET count=delivery_rate_limits.count+1 RETURNING count`, destination).Scan(&count)
	return count <= limit, err
}

func (p *Postgres) ClaimAndEnqueueEscalations(ctx context.Context, limit int, fallbackSlack, fallbackPD string) (int, error) {
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT e.event_key,a.incident_key,e.alert,e.service_metadata FROM escalations e JOIN alert_events a ON a.event_key=e.event_key WHERE e.state='pending' AND e.due_at<=now() ORDER BY e.due_at FOR UPDATE OF e SKIP LOCKED LIMIT $1`, limit)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	type escalation struct {
		eventKey, incidentKey string
		alert                 domain.Alert
		service               domain.Service
	}
	var out []escalation
	for rows.Next() {
		var e escalation
		var a, s []byte
		if err := rows.Scan(&e.eventKey, &e.incidentKey, &a, &s); err != nil {
			return 0, err
		}
		if err = json.Unmarshal(a, &e.alert); err != nil {
			return 0, err
		}
		if err = json.Unmarshal(s, &e.service); err != nil {
			return 0, err
		}
		out = append(out, e)
	}
	if rows.Err() != nil {
		return 0, rows.Err()
	}
	for _, e := range out {
		channel := e.service.EscalationSlackChannel
		if channel == "" {
			channel = fallbackSlack
		}
		pd := e.service.EscalationPagerDutyService
		if pd == "" {
			pd = e.service.PagerDutyService
		}
		if pd == "" {
			pd = fallbackPD
		}
		for _, d := range []domain.Destination{{Strategy: "slack", Target: channel, Stage: "escalation"}, {Strategy: "pagerduty", Target: pd, Stage: "escalation"}} {
			n := domain.Notification{EventKey: e.eventKey, IncidentKey: e.incidentKey, Status: "firing", Severity: domain.SeverityPage, Stage: "escalation", Destination: d.Target, Service: e.service, Alert: e.alert, Labels: e.alert.Labels, Annotations: e.alert.Annotations}
			payload, _ := json.Marshal(n)
			if _, err = tx.Exec(ctx, `INSERT INTO notification_outbox(event_key,strategy,destination,stage,payload) VALUES($1,$2,$3,'escalation',$4) ON CONFLICT DO NOTHING`, e.eventKey, d.Strategy, d.Target, payload); err != nil {
				return 0, err
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE escalations SET state='escalated',escalated_at=now(),updated_at=now() WHERE event_key=$1 AND state='pending'`, e.eventKey); err != nil {
			return 0, err
		}
	}
	return len(out), tx.Commit(ctx)
}

func (p *Postgres) Acknowledge(ctx context.Context, eventKey, actor string) (bool, error) {
	tag, err := p.Pool.Exec(ctx, `UPDATE escalations SET state='acknowledged',acknowledged_at=now(),acknowledged_by=$2,updated_at=now() WHERE event_key=$1 AND state IN ('pending','escalated')`, eventKey, actor)
	if err != nil {
		return false, err
	}
	ok := tag.RowsAffected() > 0
	if ok {
		_, err = p.Pool.Exec(ctx, `INSERT INTO audit_log(event_type,actor,outcome,detail) VALUES('alert.acknowledged',$1,'success',$2)`, actor, mapJSON(map[string]any{"event_key": eventKey}))
	}
	return ok, err
}

func (p *Postgres) EventTeam(ctx context.Context, eventKey string) (string, error) {
	var team string
	err := p.Pool.QueryRow(ctx, `SELECT team FROM alert_events WHERE event_key=$1`, eventKey).Scan(&team)
	return team, err
}

func (p *Postgres) EnqueueAcknowledgement(ctx context.Context, eventKey string) error {
	rows, err := p.Pool.Query(ctx, `SELECT DISTINCT ON(destination) payload,destination FROM notification_outbox WHERE event_key=$1 AND strategy='pagerduty' AND stage IN ('primary','escalation') ORDER BY destination,id`, eventKey)
	if err != nil {
		return err
	}
	defer rows.Close()
	type target struct {
		raw         []byte
		destination string
	}
	var targets []target
	for rows.Next() {
		var x target
		if err = rows.Scan(&x.raw, &x.destination); err != nil {
			return err
		}
		targets = append(targets, x)
	}
	if rows.Err() != nil {
		return rows.Err()
	}
	for _, x := range targets {
		var n domain.Notification
		if err = json.Unmarshal(x.raw, &n); err != nil {
			return err
		}
		n.Stage = "acknowledgement"
		n.Destination = x.destination
		payload, _ := json.Marshal(n)
		if _, err = p.Pool.Exec(ctx, `INSERT INTO notification_outbox(event_key,strategy,destination,stage,payload) VALUES($1,'pagerduty',$2,'acknowledgement',$3) ON CONFLICT DO NOTHING`, eventKey, x.destination, payload); err != nil {
			return err
		}
	}
	return nil
}

func (p *Postgres) SaveSilence(ctx context.Context, id string, m domain.MuteRequest, starts, ends time.Time) error {
	_, err := p.Pool.Exec(ctx, `INSERT INTO silences(id,service,team,created_by,reason,starts_at,ends_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, m.Service, m.Team, m.CreatedBy, m.Reason, starts, ends)
	if err != nil {
		return err
	}
	_, err = p.Pool.Exec(ctx, `INSERT INTO audit_log(event_type,actor,team,service,outcome,detail) VALUES('silence.created',$1,$2,$3,'success',$4)`, m.CreatedBy, m.Team, m.Service, mapJSON(map[string]any{"silence_id": id, "ends_at": ends, "reason": m.Reason}))
	return err
}

func (p *Postgres) Ready(ctx context.Context) error { return p.Pool.Ping(ctx) }
func mapJSON(v any) []byte                          { b, _ := json.Marshal(v); return b }

var ErrRateLimited = errors.New("delivery rate limit exceeded")
