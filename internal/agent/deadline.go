package agent

import "time"

const (
	reasonMaxLifetimeElapsed = "max lifetime elapsed"
	reasonWallClockDeadline  = "wall clock deadline passed"
)

func fired(startedAt time.Time, maxLifetime time.Duration, wall *time.Time, now time.Time) (string, bool) {
	if now.Sub(startedAt) >= maxLifetime {
		return reasonMaxLifetimeElapsed, true
	}
	if wall != nil && !now.Before(*wall) {
		return reasonWallClockDeadline, true
	}
	return "", false
}
