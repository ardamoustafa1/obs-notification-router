package service

import (
	"testing"
	"time"

	"github.com/acme/obs-notification-router/internal/domain"
)

func TestIncidentKeyDeduplicatesFlappingWithinWindow(t *testing.T) {
	base := time.Date(2026, 9, 17, 10, 1, 0, 0, time.UTC)
	a := domain.Alert{Fingerprint: "abc", StartsAt: base, Status: "firing"}
	b := domain.Alert{Fingerprint: "abc", StartsAt: base.Add(7 * time.Minute), Status: "firing"}
	if IncidentKey(a, 10*time.Minute) != IncidentKey(b, 10*time.Minute) {
		t.Fatal("same fingerprint inside dedup window must share incident key")
	}
	if eventKey(IncidentKey(a, 10*time.Minute), "firing") == eventKey(IncidentKey(a, 10*time.Minute), "resolved") {
		t.Fatal("firing and resolved outbox events must remain distinct")
	}
}

func TestIncidentKeyChangesOutsideWindow(t *testing.T) {
	base := time.Date(2026, 9, 17, 10, 1, 0, 0, time.UTC)
	a := domain.Alert{Fingerprint: "abc", StartsAt: base}
	b := domain.Alert{Fingerprint: "abc", StartsAt: base.Add(10 * time.Minute)}
	if IncidentKey(a, 10*time.Minute) == IncidentKey(b, 10*time.Minute) {
		t.Fatal("new window must create a new incident key")
	}
}
