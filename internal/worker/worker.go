package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/acme/obs-notification-router/internal/notify"
	"github.com/acme/obs-notification-router/internal/repository"
)

type Worker struct {
	repo                      *repository.Postgres
	notifications             *notify.Registry
	poll                      time.Duration
	concurrency, rateLimit    int
	fallbackSlack, fallbackPD string
	logger                    *slog.Logger
}

func New(repo *repository.Postgres, n *notify.Registry, poll time.Duration, concurrency, rateLimit int, fallbackSlack, fallbackPD string, logger *slog.Logger) *Worker {
	return &Worker{repo: repo, notifications: n, poll: poll, concurrency: concurrency, rateLimit: rateLimit, fallbackSlack: fallbackSlack, fallbackPD: fallbackPD, logger: logger}
}
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()
	w.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}
func (w *Worker) tick(ctx context.Context) {
	_, err := w.repo.ClaimAndEnqueueEscalations(ctx, 100, w.fallbackSlack, w.fallbackPD)
	if err != nil {
		w.logger.Error("claim escalations", "error", err)
	}
	jobs, err := w.repo.ClaimJobs(ctx, w.concurrency*4)
	if err != nil {
		w.logger.Error("claim notification jobs", "error", err)
		return
	}
	sem := make(chan struct{}, w.concurrency)
	var wg sync.WaitGroup
	for _, job := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(j repository.Job) {
			defer wg.Done()
			defer func() { <-sem }()
			allowed, err := w.repo.AllowDelivery(ctx, j.Notification.Destination, w.rateLimit)
			if err == nil && !allowed {
				err = repository.ErrRateLimited
			}
			if err == nil {
				delivery, sendErr := w.notifications.Send(ctx, j.Strategy, j.Notification)
				if sendErr == nil {
					err = w.repo.CompleteJob(ctx, j, delivery)
				} else {
					err = sendErr
				}
			}
			if err != nil {
				w.logger.Warn("notification failed", "strategy", j.Strategy, "destination", j.Notification.Destination, "error", err)
				if retryErr := w.repo.FailJob(ctx, j, err); retryErr != nil {
					w.logger.Error("schedule retry", "error", retryErr)
				}
			}
		}(job)
	}
	wg.Wait()
}
