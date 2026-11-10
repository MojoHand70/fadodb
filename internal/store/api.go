package store

import "time"

// Principal is who a call runs for (ADR-0027): the embedder's id for the
// actor, the groups INGROUP tests, the admin bypass, and the embedder's
// business day, the default business date of a commit (ADR-0026). A zero
// Today means none.
type Principal struct {
	ID     string
	Groups []string
	Admin  bool
	Today  time.Time
}

// Values are variable values by declared name, in the wire shape of
// ADR-0018: a blank is nil, a bool a bool, a number a float64 or any Go
// integer, an exact value text such as "12.30" or a number that is exact
// at the variable's scale, a money "12.30 PLN", a text a string, a date
// "YYYY-MM-DD" or a time.Time, a datetime integer seconds, a time.Time or
// "YYYY-MM-DD HH:MM:SS", a ref "Class:id", a json value its text, an
// object a map of its fields, rows a list of maps and an array a list of
// scalars. Names match case-insensitively. A document's Values on the way
// out use the same shape with a float64 for every number, an int64 for a
// datetime and json.RawMessage for a json value.
type Values map[string]any

// Document is a document at one version: the system variables of
// ADR-0013 and the declared variables under Values, blank as nil. Every
// variable the principal may read in the document's state is present and
// a hidden one absent (ADR-0041).
type Document struct {
	Class   string `json:"class"`
	ID      string `json:"id"`
	Title   string `json:"title"`
	State   string `json:"state,omitempty"`
	Version int64  `json:"version"`
	// ClassVersion is the version of the class that computed this version
	// of the document (ADR-0039).
	ClassVersion int `json:"class_version"`
	// Seconds since the epoch, UTC.
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
	StateEnteredAt int64  `json:"state_entered_at,omitempty"`
	Values         Values `json:"values"`
	// Computed says, for every triggered variable that has been computed
	// or has failed to, when its value was computed (ADR-0043). A variable
	// the principal may not read is absent here as it is from Values.
	Computed map[string]Moment `json:"computed,omitempty"`
}

// Moment is when a triggered variable's value was computed: the version
// of the document whose commit computed it and the time of that commit,
// in seconds since the epoch. Error is the code of a recomputation that
// failed since then, which left the value as it was; a variable that has
// only ever failed has an Error and no Version.
type Moment struct {
	Version int64  `json:"version,omitempty"`
	At      int64  `json:"at,omitempty"`
	Error   string `json:"error,omitempty"`
}

// HistoryRow is one committed version: the document as it was, when it
// was current, who committed it and the event that did (ADR-0014).
type HistoryRow struct {
	Document
	ValidFrom int64 `json:"valid_from"`
	// ValidTo is 0 while the version is current.
	ValidTo int64  `json:"valid_to,omitempty"`
	ActorID string `json:"actor_id,omitempty"`
	EventID string `json:"event_id"`
}

// Event is one row of the event log (ADR-0006, ADR-0026).
type Event struct {
	ID            string `json:"event_id"`
	Class         string `json:"class"`
	DocID         string `json:"doc_id"`
	Kind          string `json:"kind"`
	Name          string `json:"name,omitempty"`
	FromState     string `json:"from_state,omitempty"`
	ToState       string `json:"to_state,omitempty"`
	ActorID       string `json:"actor_id,omitempty"`
	BeforeVersion int64  `json:"before_version,omitempty"`
	AfterVersion  int64  `json:"after_version,omitempty"`
	CauseEventID  string `json:"cause_event_id,omitempty"`
	CommandKey    string `json:"command_key,omitempty"`
	// BusinessDate is the calendar day as YYYY-MM-DD.
	BusinessDate string `json:"business_date"`
	TS           int64  `json:"ts"`
}

// The event kinds this build writes, and the qualifiers of their
// event_docs rows.
const (
	EventCreated    = "created"
	EventSet        = "set"
	EventDeleted    = "deleted"
	EventMigrated   = "migrated"
	EventTransition = "transition"
	// EventCustom is the event a rule's emit appends (ADR-0043): its name
	// is the one the rule gives, its after_version the version of the
	// commit that ran the rule and its cause that commit's event.
	EventCustom = "custom"
)

// TransitionInfo is one named transition leaving a document's state, as
// Transitions and Preview list them (ADR-0040): its name and target,
// whether the principal may take it now and, when not, the code that
// says why, forbidden when its access rule is not TRUE for the principal
// (ADR-0041) and guard_false when its guard is not, with the rule's or
// the guard's error when it errored.
type TransitionInfo struct {
	Name    string `json:"name"`
	To      string `json:"to"`
	Enabled bool   `json:"enabled"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// Preview is what a commit would do, without having done it (ADR-0025):
// the document as it would be, and the error of every variable whose
// formula failed or whose value the principal may not set, each with its
// code; for a class with states, the states the command visited, the
// state before first, and the named transitions leaving the resulting
// state (ADR-0040); and Now, the seconds since the epoch its formulas
// saw, the moment chosen with At or the database's clock (ADR-0042). A
// preview of a set that changes nothing reports the path and the
// transitions all the same, the version as it is. Rules lists every rule
// whose trigger the command raised, in the order it would run (ADR-0043);
// an action that fails is recorded under Errors with its path and the
// preview goes on, where a commit would be refused.
type Preview struct {
	Document    *Document        `json:"document"`
	Errors      []*Error         `json:"errors,omitempty"`
	Path        []string         `json:"path,omitempty"`
	Transitions []TransitionInfo `json:"transitions,omitempty"`
	Rules       []RuleRun        `json:"rules,omitempty"`
	Now         int64            `json:"now"`
}

// RuleRun is one rule whose trigger a previewed command raised (ADR-0043):
// the commit it belongs to, 0 for the command's own and n for its n-th
// eventless transition; its path in the definition, such as /rules/2;
// whether it ran; the error code when its condition errored, which is
// FALSE; and its actions when it ran.
type RuleRun struct {
	Commit  int         `json:"commit"`
	Path    string      `json:"path"`
	Ran     bool        `json:"ran"`
	Code    string      `json:"code,omitempty"`
	Actions []ActionRun `json:"actions,omitempty"`
}

// ActionRun is one action of a rule that ran in a preview: its name,
// whether it is queued for the outbox or synchronous, and its evaluated
// arguments in the shape the action is written in: the values of a set
// by variable, the variables of a reset, the event of an emit. The
// arguments of a queued action arrive with the outbox.
type ActionRun struct {
	Action string `json:"action"`
	Queued bool   `json:"queued,omitempty"`
	Args   Values `json:"args,omitempty"`
}

// Option configures one call.
type Option func(*options)

type options struct {
	principal       Principal
	key             string
	businessDate    time.Time
	hasBusinessDate bool
	id              string
	replace         bool
	version         int64
	at              time.Time
}

// As runs the call as the principal.
func As(p Principal) Option { return func(o *options) { o.principal = p } }

// Key gives a mutating command its idempotency key (ADR-0026): a repeat
// with the same key makes no second commit and returns the first one's
// result.
func Key(k string) Option { return func(o *options) { o.key = k } }

// BusinessDate gives the commit's event its business date (ADR-0026), a
// calendar day; the time of day and the zone are ignored.
func BusinessDate(day time.Time) Option {
	return func(o *options) { o.businessDate, o.hasBusinessDate = day, true }
}

// WithID gives a new document the caller's id instead of a generated one.
func WithID(id string) Option { return func(o *options) { o.id = id } }

// Replace makes an import empty the table first, so that the rows given
// are all the rows.
func Replace() Option { return func(o *options) { o.replace = true } }

// AtVersion makes OpenFile read the document at a committed version
// instead of the current one.
func AtVersion(version int64) Option { return func(o *options) { o.version = version } }

// At runs a call that reads or previews at the moment (ADR-0042): NOW()
// is t and TODAY() its UTC day in every formula the call evaluates,
// defaults, guards, access rules and rights alike, and a preview reports
// it as Now. A zero t is the database's clock. A command that writes
// refuses it with not_allowed; the business date is untouched.
func At(t time.Time) Option { return func(o *options) { o.at = t } }

func apply(opts []Option) options {
	var o options
	for _, f := range opts {
		f(&o)
	}
	return o
}
