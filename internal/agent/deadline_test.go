package agent

import (
	"testing"
	"time"
)

const century = 100 * 365 * 24 * time.Hour

func TestFired(t *testing.T) {
	startedAt := time.Now()
	const maxLifetime = time.Hour

	at := func(d time.Duration) time.Time { return startedAt.Add(d) }
	wallAt := func(d time.Duration) *time.Time {
		deadline := at(d)
		return &deadline
	}

	tests := []struct {
		name       string
		wall       *time.Time
		now        time.Time
		wantReason string
		wantFired  bool
	}{
		{
			name: "backstop alone has not elapsed",
			now:  at(59 * time.Minute),
		},
		{
			name:       "backstop alone elapses exactly on the boundary",
			now:        at(maxLifetime),
			wantReason: reasonMaxLifetimeElapsed,
			wantFired:  true,
		},
		{
			name:       "backstop alone is overdue",
			now:        at(3 * maxLifetime),
			wantReason: reasonMaxLifetimeElapsed,
			wantFired:  true,
		},
		{
			name:       "a wall clock deadline earlier than the backstop shortens it",
			wall:       wallAt(10 * time.Minute),
			now:        at(11 * time.Minute),
			wantReason: reasonWallClockDeadline,
			wantFired:  true,
		},
		{
			name: "a wall clock deadline earlier than the backstop has not passed yet",
			wall: wallAt(10 * time.Minute),
			now:  at(9 * time.Minute),
		},
		{
			name:       "a wall clock deadline later than the backstop cannot defer it",
			wall:       wallAt(6 * time.Hour),
			now:        at(maxLifetime),
			wantReason: reasonMaxLifetimeElapsed,
			wantFired:  true,
		},
		{
			name:       "a wall clock deadline already in the past fires on the first read",
			wall:       wallAt(-time.Hour),
			now:        startedAt,
			wantReason: reasonWallClockDeadline,
			wantFired:  true,
		},
		{
			name: "no wall clock deadline leaves the backstop alone",
			now:  at(maxLifetime - time.Nanosecond),
		},
		{
			name:       "a wall clock a century behind cannot postpone the backstop",
			wall:       wallAt(-century),
			now:        at(maxLifetime),
			wantReason: reasonMaxLifetimeElapsed,
			wantFired:  true,
		},
		{
			name:       "a wall clock a century ahead cannot postpone the backstop",
			wall:       wallAt(century),
			now:        at(maxLifetime),
			wantReason: reasonMaxLifetimeElapsed,
			wantFired:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reason, gotFired := fired(startedAt, maxLifetime, tc.wall, tc.now)
			if gotFired != tc.wantFired {
				t.Fatalf("fired = %v, want %v", gotFired, tc.wantFired)
			}
			if reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", reason, tc.wantReason)
			}
		})
	}
}
