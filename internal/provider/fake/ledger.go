package fake

import (
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/lucawalz/horizon/internal/provider"
)

type EventKind string

const (
	EventCreate EventKind = "create"
	EventDelete EventKind = "delete"
)

type LedgerEvent struct {
	Kind      EventKind
	Name      string
	At        time.Time
	ExpiresAt time.Time
}

func (e LedgerEvent) String() string {
	expiry := "no deadline"
	if !e.ExpiresAt.IsZero() {
		expiry = "expires " + e.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return fmt.Sprintf("%s %s at %s (%s)", e.Kind, e.Name, e.At.UTC().Format(time.RFC3339), expiry)
}

type Ledger struct {
	now    func() time.Time
	mu     sync.Mutex
	events []LedgerEvent
}

func newLedger(now func() time.Time) *Ledger {
	return &Ledger{now: now}
}

func (l *Ledger) Events() []LedgerEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.events)
}

func (l *Ledger) Leaks() []LedgerEvent {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()
	undeleted := map[string]int{}
	for i, e := range l.events {
		switch e.Kind {
		case EventCreate:
			undeleted[e.Name] = i
		case EventDelete:
			delete(undeleted, e.Name)
		}
	}
	leaks := make([]LedgerEvent, 0, len(undeleted))
	for _, i := range slices.Sorted(maps.Values(undeleted)) {
		e := l.events[i]
		if !e.ExpiresAt.IsZero() && e.ExpiresAt.After(now) {
			continue
		}
		leaks = append(leaks, e)
	}
	return leaks
}

func (l *Ledger) record(kind EventKind, inst provider.Instance, at time.Time) {
	expiry, _ := provider.ParseExpiry(inst.Labels)

	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, LedgerEvent{Kind: kind, Name: inst.Name, At: at, ExpiresAt: expiry})
}
