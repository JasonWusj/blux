package automation

import (
	"testing"
	"time"
)

func at(t *testing.T, loc *time.Location, value string) time.Time {
	t.Helper()
	parsed, err := time.ParseInLocation("2006-01-02 15:04", value, loc)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestLatestSlotChoosesTheMostRecentPassedSlot(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)
	for _, test := range []struct{ name, now, want string }{
		{"before today's time uses yesterday", "2026-11-03 08:59", "2026-11-02 09:00"},
		{"exactly at the time counts as due", "2026-11-03 09:00", "2026-11-03 09:00"},
		{"after today's time uses today", "2026-11-03 23:30", "2026-11-03 09:00"},
		{"month boundary", "2026-11-01 00:10", "2026-10-31 09:00"},
		{"year boundary", "2027-01-01 08:00", "2026-12-31 09:00"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, ok := LatestSlot("09:00", at(t, loc, test.now), loc)
			if !ok || !got.Equal(at(t, loc, test.want)) {
				t.Fatalf("got %v ok=%v, want %s", got, ok, test.want)
			}
		})
	}
}

// A machine that slept through many days must still produce exactly one slot,
// the latest one, never a backlog.
func TestLatestSlotNeverReturnsABacklog(t *testing.T) {
	loc := time.UTC
	got, _ := LatestSlot("09:00", at(t, loc, "2026-11-10 12:00"), loc)
	if !got.Equal(at(t, loc, "2026-11-10 09:00")) {
		t.Fatal(got)
	}
}

func TestNextSlot(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)
	for _, test := range []struct{ now, want string }{
		{"2026-11-03 08:59", "2026-11-03 09:00"},
		{"2026-11-03 09:00", "2026-11-04 09:00"},
		{"2026-11-03 23:59", "2026-11-04 09:00"},
		{"2026-12-31 10:00", "2027-01-01 09:00"},
	} {
		got, ok := NextSlot("09:00", at(t, loc, test.now), loc)
		if !ok || !got.Equal(at(t, loc, test.want)) {
			t.Fatalf("now=%s got %v ok=%v, want %s", test.now, got, ok, test.want)
		}
	}
}

func TestSlotsRejectInvalidClocks(t *testing.T) {
	for _, clock := range []string{"", "9:00", "24:00", "09:60", "ab:cd", "09-00", "09:000"} {
		if _, ok := LatestSlot(clock, time.Now(), time.UTC); ok {
			t.Fatalf("LatestSlot accepted %q", clock)
		}
		if _, ok := NextSlot(clock, time.Now(), time.UTC); ok {
			t.Fatalf("NextSlot accepted %q", clock)
		}
	}
}

// The key identifies the local date and time of a slot, and differs for
// different automations, so one slot can never start twice.
func TestSlotKeyIsStablePerSlotAndAutomation(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)
	slot := at(t, loc, "2026-11-03 09:00")
	if SlotKey("a", slot) != "automation:a:slot:2026-11-03T09:00" {
		t.Fatal(SlotKey("a", slot))
	}
	if SlotKey("a", slot) == SlotKey("b", slot) {
		t.Fatal("different automations share a key")
	}
	if SlotKey("a", slot) == SlotKey("a", slot.Add(24*time.Hour)) {
		t.Fatal("different days share a key")
	}
}

// Across a daylight-saving jump the slot still lands on the requested wall
// clock time, and consecutive days get different keys.
func TestSlotsFollowWallClockAcrossDaylightSaving(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("time zone database unavailable:", err)
	}
	// 2026-11-01 is the day clocks go back in New York.
	before, _ := NextSlot("09:00", at(t, loc, "2026-10-31 10:00"), loc)
	after, _ := NextSlot("09:00", at(t, loc, "2026-11-01 10:00"), loc)
	if before.Hour() != 9 || after.Hour() != 9 {
		t.Fatal(before, after)
	}
	if SlotKey("a", before) == SlotKey("a", after) {
		t.Fatal("daylight-saving day reused a key")
	}
}

func TestDayStartIsFixedWidthAndLocalMidnight(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)
	got := DayStart(at(t, loc, "2026-11-03 15:30"), loc)
	if got != "2026-11-02T16:00:00.000000000Z" {
		t.Fatal(got)
	}
	// A run created one nanosecond after local midnight, as stored by core.Now,
	// must compare as not earlier than the day start.
	created := at(t, loc, "2026-11-03 00:00").UTC().Add(time.Nanosecond).Format(time.RFC3339Nano)
	if created < got {
		t.Fatalf("run %s sorts before day start %s", created, got)
	}
	exact := at(t, loc, "2026-11-03 00:00").UTC().Format(time.RFC3339Nano)
	if exact < got {
		t.Fatalf("run at exactly midnight %s sorts before day start %s", exact, got)
	}
}
