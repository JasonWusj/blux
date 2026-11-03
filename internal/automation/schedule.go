// Package automation runs saved prompts on a schedule or on demand. It turns a
// due automation into an ordinary Blux task, watches that task finish, and
// records the outcome for the inbox.
package automation

import (
	"blux/internal/core"
	"time"
)

const slotLayout = "2006-01-02T15:04"

// LatestSlot returns the most recent scheduled instant at or before now. When
// the machine was off across several slots, only this one is ever run.
func LatestSlot(clock string, now time.Time, loc *time.Location) (time.Time, bool) {
	hour, minute, ok := core.ParseClock(clock)
	if !ok {
		return time.Time{}, false
	}
	local := now.In(loc)
	slot := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, loc)
	if slot.After(local) {
		slot = time.Date(local.Year(), local.Month(), local.Day()-1, hour, minute, 0, 0, loc)
	}
	return slot, true
}

// NextSlot returns the first scheduled instant after now.
func NextSlot(clock string, now time.Time, loc *time.Location) (time.Time, bool) {
	hour, minute, ok := core.ParseClock(clock)
	if !ok {
		return time.Time{}, false
	}
	local := now.In(loc)
	slot := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, loc)
	if !slot.After(local) {
		slot = time.Date(local.Year(), local.Month(), local.Day()+1, hour, minute, 0, 0, loc)
	}
	return slot, true
}

// SlotKey names one scheduled slot of one automation. A slot is unique per
// local date and time, so repeated ticks, restarts, and a worker running twice
// can never start the same slot twice.
func SlotKey(automationID string, slot time.Time) string {
	return "automation:" + automationID + ":slot:" + slot.Format(slotLayout)
}

// DayStart returns the first instant of now's local day as a fixed-width UTC
// timestamp, which compares correctly as text against stored timestamps.
func DayStart(now time.Time, loc *time.Location) string {
	local := now.In(loc)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	return start.UTC().Format("2006-01-02T15:04:05.000000000Z")
}
