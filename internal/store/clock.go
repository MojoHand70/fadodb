package store

import "time"

// A moment the caller chooses (ADR-0042). At sets the clock of a call
// that reads or previews and writes nothing: NOW() is the instant and
// TODAY() its UTC day in every formula the call evaluates, defaults,
// guards, access rules and rights alike. A commit runs at the database's
// clock, and At on one is refused with not_allowed: a change is previewed
// at a moment, or made in a copy of the database opened with Options.Now
// set. At touches no business date, which is the principal's fact.

// clock is the seconds a call's formulas see: the moment chosen with At,
// else the database's clock.
func (s *Store) clock(o options) int64 {
	if !o.at.IsZero() {
		return o.at.Unix()
	}
	return s.now().Unix()
}

// refuseAt is the refusal of At on a command that writes.
func refuseAt(o options, what string) *Error {
	if o.at.IsZero() {
		return nil
	}
	return hintf(CodeNotAllowed, "", "a "+what+" runs at the database's clock; At chooses the moment of a preview or a read (ADR-0042)",
		"preview the change with At("+o.at.UTC().Format(time.DateTime)+"), or make it in a copy of the database opened with Options.Now set")
}
