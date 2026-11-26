package model

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is when a triggered variable that lists schedule:<words> is
// worked out again (ADR-0093): the moments of a day, a week or a month,
// written in words, by the clock of the database's own time zone.
//
//	hourly
//	daily                  daily at 03:00
//	weekly                 weekly on friday at 18:00
//	monthly                monthly on 15
//	monthly on last day    monthly on 3rd monday at 06:00
//	every 15m              every 4h
//
// Told no time, a schedule fires at midnight; told no day, a weekly one
// fires on Monday and a monthly one on the first. A length after every
// is written as a transition's after is, in minutes or hours, and
// divides a day, so that it fires at the same times every day, counted
// from midnight.
type Schedule struct {
	// Text is the schedule as the class keeps it: in lower case, with
	// single spaces.
	Text string

	kind         scheduleKind
	hour, minute int
	// weekday is the day of a weekly schedule, and of a monthly one that
	// names the nth of a weekday; nth is 1 to 4, or -1 for the last.
	weekday time.Weekday
	nth     int
	// day is the day of a monthly schedule, 1 to 28, or -1 for the last
	// day; 0 when the schedule names a weekday.
	day int
	// every is the length of an every schedule, in seconds.
	every int
}

type scheduleKind uint8

const (
	scheduleHourly scheduleKind = iota
	scheduleDaily
	scheduleWeekly
	scheduleMonthly
	scheduleEvery
)

// MinEvery is the shortest length an every schedule takes, in seconds:
// five minutes. A schedule looks at every open document of its class.
const MinEvery = 5 * 60

var weekdays = map[string]time.Weekday{
	"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday, "wednesday": time.Wednesday,
	"thursday": time.Thursday, "friday": time.Friday, "saturday": time.Saturday,
}

var nths = map[string]int{"1st": 1, "2nd": 2, "3rd": 3, "4th": 4, "last": -1}

// scheduleHint says how a schedule is written, for a finding.
const scheduleHint = `one of: "hourly"; "daily" or "daily at 03:00"; "weekly" or "weekly on friday at 18:00"; "monthly", "monthly on 15", "monthly on last day" or "monthly on 3rd monday at 06:00"; "every 15m" or "every 4h"`

// ParseSchedule reads the words after schedule: in a trigger.
func ParseSchedule(text string) (*Schedule, error) {
	words := strings.Fields(strings.ToLower(text))
	if len(words) == 0 {
		return nil, fmt.Errorf("a schedule says when")
	}
	s := &Schedule{Text: strings.Join(words, " ")}
	rest := words[1:]
	// at takes the time that ends a schedule, when it has one.
	at := func() error {
		if len(rest) == 0 {
			return nil
		}
		if rest[0] != "at" || len(rest) != 2 {
			return fmt.Errorf("after %q comes \"at\" and a time, as \"at 03:00\"", strings.Join(words[:len(words)-len(rest)], " "))
		}
		h, m, ok := strings.Cut(rest[1], ":")
		hour, herr := strconv.Atoi(h)
		minute, merr := strconv.Atoi(m)
		if !ok || herr != nil || merr != nil || len(m) != 2 || len(h) == 0 || len(h) > 2 || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
			return fmt.Errorf("a time is written as hours and minutes of a day of 24 hours, \"03:00\" or \"18:30\"; got %q", rest[1])
		}
		s.hour, s.minute = hour, minute
		rest = nil
		return nil
	}
	switch words[0] {
	case "hourly":
		s.kind = scheduleHourly
		if len(rest) > 0 {
			return nil, fmt.Errorf("hourly fires at every full hour and takes nothing more; for other minutes write \"every 30m\"")
		}
	case "daily":
		s.kind = scheduleDaily
		if err := at(); err != nil {
			return nil, err
		}
	case "weekly":
		s.kind, s.weekday = scheduleWeekly, time.Monday
		if len(rest) >= 2 && rest[0] == "on" {
			day, ok := weekdays[rest[1]]
			if !ok {
				return nil, fmt.Errorf("after \"weekly on\" comes a day of the week, as \"friday\"; got %q", rest[1])
			}
			s.weekday, rest = day, rest[2:]
		}
		if err := at(); err != nil {
			return nil, err
		}
	case "monthly":
		s.kind, s.day = scheduleMonthly, 1
		if len(rest) >= 2 && rest[0] == "on" {
			switch n, isNth := nths[rest[1]]; {
			case rest[1] == "last" && len(rest) >= 3 && rest[2] == "day":
				s.day, rest = -1, rest[3:]
			case isNth:
				if len(rest) < 3 {
					return nil, fmt.Errorf("after \"monthly on %s\" comes a day of the week, or \"day\" after \"last\"", rest[1])
				}
				day, ok := weekdays[rest[2]]
				if !ok {
					return nil, fmt.Errorf("after \"monthly on %s\" comes a day of the week, as \"monday\"; got %q", rest[1], rest[2])
				}
				s.day, s.nth, s.weekday, rest = 0, n, day, rest[3:]
			default:
				day, err := strconv.Atoi(rest[1])
				if err != nil || day < 1 || day > 28 {
					return nil, fmt.Errorf("a monthly schedule names a day from 1 to 28, \"last day\", or the 1st, 2nd, 3rd, 4th or last of a weekday; got %q", rest[1])
				}
				s.day, rest = day, rest[2:]
			}
		}
		if err := at(); err != nil {
			return nil, err
		}
	case "every":
		s.kind = scheduleEvery
		if len(rest) != 1 {
			return nil, fmt.Errorf("after \"every\" comes one length of time, as \"15m\" or \"4h\"")
		}
		secs, ok := ParseAfter(rest[0])
		unit := rest[0][len(rest[0])-1]
		switch {
		case !ok || (unit != 'm' && unit != 'h'):
			return nil, fmt.Errorf("the length after \"every\" is a whole number of minutes or hours, as \"15m\" or \"4h\"; got %q", rest[0])
		case secs < MinEvery:
			return nil, fmt.Errorf("the shortest schedule is every 5m: it looks at every open document of the class; got %q", rest[0])
		case 86400%secs != 0:
			return nil, fmt.Errorf("the length after \"every\" divides a day, so that the schedule fires at the same times every day, as 15m, 30m, 1h, 4h or 12h; got %q", rest[0])
		}
		s.every = int(secs)
	default:
		return nil, fmt.Errorf("a schedule begins with hourly, daily, weekly, monthly or every; got %q", words[0])
	}
	return s, nil
}

// Next is the first moment of the schedule after a moment, by the clock
// of the zone. A time of day that a day does not have, in the hour a
// zone's clocks skip in spring, fires when that hour would have ended;
// one a day has twice, in autumn, fires the first time.
func (s *Schedule) Next(after time.Time, zone *time.Location) time.Time {
	local := after.In(zone)
	y, m, d := local.Date()
	at := func(y int, m time.Month, d int) time.Time {
		return wall(y, m, d, s.hour, s.minute, 0, zone)
	}
	switch s.kind {
	case scheduleHourly:
		for h := local.Hour(); ; h++ {
			if t := wall(y, m, d, h, 0, 0, zone); t.After(after) {
				return t
			}
		}
	case scheduleEvery:
		for secs := 0; ; secs += s.every {
			if t := wall(y, m, d, 0, 0, secs, zone); t.After(after) {
				return t
			}
		}
	case scheduleDaily:
		for day := d; ; day++ {
			if t := at(y, m, day); t.After(after) {
				return t
			}
		}
	case scheduleWeekly:
		for day := d; ; day++ {
			if t := at(y, m, day); t.Weekday() == s.weekday && t.After(after) {
				return t
			}
		}
	}
	// Monthly: the day of this month, then of the next ones.
	for month := m; ; month++ {
		if t := at(y, month, s.dayOf(y, month, zone)); t.After(after) {
			return t
		}
	}
}

// wall is the moment a clock of the zone shows a time of a day: the
// first, on the day a time comes twice because the clocks go back. The
// standard library says only that it answers one of the two.
func wall(y int, m time.Month, d, hour, minute, sec int, zone *time.Location) time.Time {
	t := time.Date(y, m, d, hour, minute, sec, 0, zone)
	shown := func(t time.Time) [6]int {
		t = t.In(zone)
		return [6]int{t.Year(), int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second()}
	}
	for _, back := range [...]time.Duration{2 * time.Hour, time.Hour, 30 * time.Minute} {
		if earlier := t.Add(-back); shown(earlier) == shown(t) {
			return earlier
		}
	}
	return t
}

// dayOf is the day of a month a monthly schedule fires on. The month may
// be past December: it is counted on into the next year.
func (s *Schedule) dayOf(y int, m time.Month, zone *time.Location) int {
	first := time.Date(y, m, 1, 12, 0, 0, 0, zone)
	last := time.Date(y, m+1, 0, 12, 0, 0, 0, zone).Day()
	switch {
	case s.day > 0:
		return s.day
	case s.day < 0:
		return last
	case s.nth > 0:
		return 1 + (int(s.weekday)-int(first.Weekday())+7)%7 + 7*(s.nth-1)
	}
	// The last of a weekday.
	end := time.Date(y, m, last, 12, 0, 0, 0, zone)
	return last - (int(end.Weekday())-int(s.weekday)+7)%7
}
