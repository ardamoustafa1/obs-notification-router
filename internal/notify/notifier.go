package notify

import (
	"context"
	"errors"
	"fmt"

	"github.com/acme/obs-notification-router/internal/domain"
)

type Strategy interface {
	Name() string
	Send(context.Context, domain.Notification) (domain.DeliveryResult, error)
}

type Registry struct{ strategies map[string]Strategy }

func NewRegistry(strategies ...Strategy) *Registry {
	r := &Registry{strategies: make(map[string]Strategy, len(strategies))}
	for _, s := range strategies {
		r.strategies[s.Name()] = s
	}
	return r
}

func (r *Registry) Send(ctx context.Context, strategy string, n domain.Notification) (domain.DeliveryResult, error) {
	s, ok := r.strategies[strategy]
	if !ok {
		return domain.DeliveryResult{}, fmt.Errorf("%w: %s", ErrUnknownStrategy, strategy)
	}
	return s.Send(ctx, n)
}

var ErrUnknownStrategy = errors.New("unknown notification strategy")
