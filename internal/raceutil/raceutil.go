// Package raceutil provides deterministic race number calculation based on UTC time.
// Race schedules are defined in Malta local time (Europe/Malta) and converted to UTC
// dynamically — handles CET/CEST transitions automatically without restart.
// The date in gameId is always CET (UTC+1 fixed, not CEST).
package raceutil

import (
	"fmt"
	"math"
	"time"

	"vg-racegen/internal/config"
)

// maltaLoc is the Europe/Malta timezone.
var maltaLoc *time.Location

// Malta local start times for each game type (hours, minutes, seconds from midnight Malta).
var maltaStarts = map[string]struct {
	H, M, S     int
	IntervalSec int
}{
	"dog6":          {0, 0, 0, 240},
	"dog63":         {0, 0, 0, 240},
	"dog8":          {0, 3, 30, 240},
	"horse":         {0, 0, 0, 320},
	"horse_classic": {0, 1, 0, 240}, // betoffer 241 (4min), epoch 00:01 Malta — verified against vendor gameResult of race 257 (videoStart 15:05 UTC → epoch 22:01 UTC yesterday = 00:01 Malta CEST)
}

func init() {
	var err error
	maltaLoc, err = time.LoadLocation("Europe/Malta")
	if err != nil {
		maltaLoc = time.FixedZone("CET", 3600)
	}
}

// scheduleStartUTCAt converts a Malta local start time to UTC for a given moment.
// This handles CET/CEST automatically — no cached offset.
func scheduleStartUTCAt(gameType string, at time.Time) (hour, min, sec, intervalSec int, ok bool) {
	ms, exists := maltaStarts[gameType]
	if !exists {
		return 0, 0, 0, 0, false
	}

	// Build the Malta local time for "today at start time".
	maltaTime := at.In(maltaLoc)
	maltaStart := time.Date(maltaTime.Year(), maltaTime.Month(), maltaTime.Day(),
		ms.H, ms.M, ms.S, 0, maltaLoc)

	// Convert to UTC.
	utcStart := maltaStart.UTC()

	return utcStart.Hour(), utcStart.Minute(), utcStart.Second(), ms.IntervalSec, true
}

// getScheduleStartForDate returns the epoch (schedule start UTC) for the current 24h cycle.
// If now is before today's schedule start, returns yesterday's.
func getScheduleStartForDate(now time.Time, gameType string) time.Time {
	h, m, s, _, ok := scheduleStartUTCAt(gameType, now)
	if !ok {
		return now
	}

	now = now.UTC()
	start := time.Date(now.Year(), now.Month(), now.Day(), h, m, s, 0, time.UTC)

	if now.Before(start) {
		start = start.AddDate(0, 0, -1)
	}
	return start
}

// CurrentRaceNumber returns the current race number (1-based) for a game type.
func CurrentRaceNumber(gameType string, now time.Time) int {
	_, _, _, intervalSec, ok := scheduleStartUTCAt(gameType, now)
	if !ok {
		return 0
	}

	schedStart := getScheduleStartForDate(now, gameType)
	elapsed := now.UTC().Sub(schedStart).Seconds()
	if elapsed < 0 {
		return 1
	}

	return int(math.Floor(elapsed/float64(intervalSec))) + 1
}

// CurrentRoundCode returns the full round code for the current race.
// Date in the code is CET (UTC+1 fixed), matching DS vendor convention.
func CurrentRoundCode(gameType string, now time.Time) string {
	gc, ok := config.GAME_TYPES[gameType]
	if !ok {
		return ""
	}

	raceNum := CurrentRaceNumber(gameType, now)

	// Date in round code is always epoch date + 1 day.
	// Epoch 2026-04-08 22:00 UTC → all races get date 2026-04-09.
	schedStart := getScheduleStartForDate(now, gameType)
	datePart := schedStart.AddDate(0, 0, 1).Format("20060102")

	return fmt.Sprintf("%d_%d_%s%04d", gc.BetofferID, gc.ScheduleID, datePart, raceNum)
}

// RaceWindow returns the round codes for a window around the current race.
func RaceWindow(gameType string, now time.Time, history, future int) []string {
	gc, ok := config.GAME_TYPES[gameType]
	if !ok {
		return nil
	}

	currentNum := CurrentRaceNumber(gameType, now)

	start := currentNum - history
	if start < 1 {
		start = 1
	}
	end := currentNum + future

	// Date in round code is always epoch date + 1 day.
	schedStart := getScheduleStartForDate(now, gameType)
	datePart := schedStart.AddDate(0, 0, 1).Format("20060102")

	codes := make([]string, 0, end-start+1)
	for n := start; n <= end; n++ {
		code := fmt.Sprintf("%d_%d_%s%04d", gc.BetofferID, gc.ScheduleID, datePart, n)
		codes = append(codes, code)
	}
	return codes
}

// VideoStartTime returns the exact video start time (UTC) for a given race number.
func VideoStartTime(gameType string, raceNumber int, now time.Time) time.Time {
	_, _, _, intervalSec, ok := scheduleStartUTCAt(gameType, now)
	if !ok {
		return time.Time{}
	}

	schedStart := getScheduleStartForDate(now, gameType)
	slotIndex := raceNumber - 1
	return schedStart.Add(time.Duration(slotIndex*intervalSec) * time.Second)
}

// Epoch returns the schedule start for dog6 (for external use/debugging).
func Epoch(now time.Time) time.Time {
	return getScheduleStartForDate(now, "dog6")
}
