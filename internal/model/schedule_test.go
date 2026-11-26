package model

import (
	"strings"
	"testing"
	"time"

	// The time zones, for a machine that has no database of them.
	_ "time/tzdata"
)

// The words of a schedule (ADR-0093), and the moments they name by the
// clock of a zone.
func TestParseSchedule(t *testing.T) {
	for _, tc := range []struct{ text, kept string }{
		{"hourly", "hourly"},
		{"daily", "daily"},
		{"Daily  at 03:00", "daily at 03:00"},
		{"daily at 3:05", "daily at 3:05"},
		{"weekly", "weekly"},
		{"weekly on friday at 18:00", "weekly on friday at 18:00"},
		{"weekly at 06:30", "weekly at 06:30"},
		{"monthly", "monthly"},
		{"monthly on 15", "monthly on 15"},
		{"monthly on last day at 23:00", "monthly on last day at 23:00"},
		{"monthly on 3rd Monday", "monthly on 3rd monday"},
		{"monthly on last friday at 17:00", "monthly on last friday at 17:00"},
		{"every 15m", "every 15m"},
		{"every 4h", "every 4h"},
		{"every 45m", "every 45m"},
	} {
		s, err := ParseSchedule(tc.text)
		if err != nil {
			t.Errorf("%q: %v", tc.text, err)
			continue
		}
		if s.Text != tc.kept {
			t.Errorf("%q is kept as %q, want %q", tc.text, s.Text, tc.kept)
		}
	}
	for _, tc := range []struct{ text, says string }{
		{"", "says when"},
		{"nightly", "begins with"},
		{"hourly at 00:30", "every 30m"},
		{"daily 03:00", `"at" and a time`},
		{"daily at 3am", "hours and minutes"},
		{"daily at 24:00", "hours and minutes"},
		{"daily at 03:60", "hours and minutes"},
		{"daily at 03:00 and 15:00", `"at" and a time`},
		{"weekly on someday", "a day of the week"},
		{"monthly on 31", "from 1 to 28"},
		{"monthly on 0", "from 1 to 28"},
		{"monthly on 5th monday", "from 1 to 28"},
		{"monthly on 3rd", "a day of the week"},
		{"monthly on 3rd day", "a day of the week"},
		{"every", "one length of time"},
		{"every 2d", "minutes or hours"},
		{"every 90s", "minutes or hours"},
		{"every 1m", "every 5m"},
		{"every 7h", "divides a day"},
		{"every 50m", "divides a day"},
		{"every 4h at 01:00", "one length of time"},
	} {
		if _, err := ParseSchedule(tc.text); err == nil || !strings.Contains(err.Error(), tc.says) {
			t.Errorf("%q: %v, want a finding that says %q", tc.text, err, tc.says)
		}
	}
}

func TestSchedule_Next(t *testing.T) {
	warsaw, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		t.Fatal(err)
	}
	at := func(zone *time.Location, text string) time.Time {
		t.Helper()
		got, err := time.ParseInLocation("2006-01-02 15:04", text, zone)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	for _, tc := range []struct {
		schedule, after string
		zone            *time.Location
		want            []string
	}{
		// 26 November 2026 is a Thursday.
		{"hourly", "2026-11-26 10:15", warsaw, []string{"2026-11-26 11:00", "2026-11-26 12:00"}},
		{"daily", "2026-11-26 10:15", warsaw, []string{"2026-11-27 00:00", "2026-11-28 00:00"}},
		{"daily at 03:00", "2026-11-26 02:59", warsaw, []string{"2026-11-26 03:00", "2026-11-27 03:00"}},
		{"daily at 03:00", "2026-11-26 03:00", warsaw, []string{"2026-11-27 03:00"}},
		{"weekly", "2026-11-26 10:15", warsaw, []string{"2026-11-30 00:00", "2026-12-07 00:00"}},
		{"weekly on friday at 18:00", "2026-11-26 10:15", warsaw, []string{"2026-11-27 18:00", "2026-12-04 18:00"}},
		{"weekly on thursday at 09:00", "2026-11-26 10:15", warsaw, []string{"2026-12-03 09:00"}},
		{"monthly", "2026-11-26 10:15", warsaw, []string{"2026-12-01 00:00", "2027-01-01 00:00"}},
		{"monthly on 15", "2026-12-20 10:15", warsaw, []string{"2027-01-15 00:00", "2027-02-15 00:00"}},
		{"monthly on last day at 23:00", "2027-01-31 23:00", warsaw, []string{"2027-02-28 23:00", "2027-03-31 23:00"}},
		// The third Monday: 21 December 2026, 18 January 2027.
		{"monthly on 3rd monday", "2026-11-26 10:15", warsaw, []string{"2026-12-21 00:00", "2027-01-18 00:00"}},
		{"monthly on 3rd monday at 06:00", "2026-11-16 05:59", warsaw, []string{"2026-11-16 06:00", "2026-12-21 06:00"}},
		// The last Friday: 27 November, 25 December 2026.
		{"monthly on last friday at 17:00", "2026-11-26 10:15", warsaw, []string{"2026-11-27 17:00", "2026-12-25 17:00"}},
		{"monthly on 1st sunday", "2026-11-26 10:15", warsaw, []string{"2026-12-06 00:00"}},
		{"every 4h", "2026-11-26 10:15", warsaw, []string{"2026-11-26 12:00", "2026-11-26 16:00", "2026-11-26 20:00", "2026-11-27 00:00"}},
		{"every 15m", "2026-11-26 23:50", warsaw, []string{"2026-11-27 00:00", "2026-11-27 00:15"}},
		// By the clock of another zone the same instant is another day.
		{"daily at 03:00", "2026-11-26 02:30", time.UTC, []string{"2026-11-26 03:00"}},
	} {
		s, err := ParseSchedule(tc.schedule)
		if err != nil {
			t.Fatal(tc.schedule, err)
		}
		moment := at(tc.zone, tc.after)
		for _, want := range tc.want {
			moment = s.Next(moment, tc.zone)
			if got := moment.In(tc.zone).Format("2006-01-02 15:04"); got != want {
				t.Errorf("%s after %s: %s, want %s", tc.schedule, tc.after, got, want)
				break
			}
		}
	}

	// 3 in the night in Warsaw is 2 in winter and 1 in summer by the
	// clock the engine keeps: the schedule goes by the zone.
	daily, _ := ParseSchedule("daily at 03:00")
	if got := daily.Next(at(warsaw, "2026-01-10 12:00"), warsaw).UTC().Format("15:04"); got != "02:00" {
		t.Errorf("3 in the night of a winter day in Warsaw is %s UTC", got)
	}
	if got := daily.Next(at(warsaw, "2026-07-10 12:00"), warsaw).UTC().Format("15:04"); got != "01:00" {
		t.Errorf("3 in the night of a summer day in Warsaw is %s UTC", got)
	}
	// The night the clocks go forward, 29 March 2026, has no 02:30: the
	// schedule fires when that hour would have ended, once. The night
	// they go back, 25 October 2026, has 02:30 twice: it fires the first
	// time.
	half, _ := ParseSchedule("daily at 02:30")
	spring := half.Next(at(warsaw, "2026-03-28 12:00"), warsaw)
	if got := spring.In(warsaw).Format("2006-01-02 15:04"); got != "2026-03-29 03:30" {
		t.Errorf("02:30 of the night the clocks go forward: %s", got)
	}
	if got := half.Next(spring, warsaw).In(warsaw).Format("2006-01-02 15:04"); got != "2026-03-30 02:30" {
		t.Errorf("the day after: %s", got)
	}
	autumn := half.Next(at(warsaw, "2026-10-24 12:00"), warsaw)
	if got := autumn.UTC().Format("2006-01-02 15:04"); got != "2026-10-25 00:30" {
		t.Errorf("02:30 of the night the clocks go back, in UTC: %s", got)
	}
	if got := half.Next(autumn, warsaw).In(warsaw).Format("2006-01-02 15:04"); got != "2026-10-26 02:30" {
		t.Errorf("the day after: %s", got)
	}
	// Every hour of the two nights: no moment twice, and each later
	// than the one before.
	hourly, _ := ParseSchedule("hourly")
	for _, night := range []string{"2026-03-29 00:30", "2026-10-25 00:30"} {
		moment := at(warsaw, night)
		for i := 0; i < 5; i++ {
			next := hourly.Next(moment, warsaw)
			if !next.After(moment) {
				t.Fatalf("hourly, the night of %s: %s is not after %s", night, next, moment)
			}
			moment = next
		}
	}
}
