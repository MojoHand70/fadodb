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
// ADR-0013 and the declared variables under Values, every variable
// present, blank as nil.
type Document struct {
	Class   string `json:"class"`
	ID      string `json:"id"`
	Title   string `json:"title"`
	State   string `json:"state,omitempty"`
	Version int64  `json:"version"`
	// Seconds since the epoch, UTC.
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
	StateEnteredAt int64  `json:"state_entered_at,omitempty"`
	Values         Values `json:"values"`
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
	EventCreated = "created"
	EventSet     = "set"
	EventDeleted = "deleted"
)

// Preview is what a commit would do, without having done it (ADR-0025):
// the document as it would be, and the error of every variable whose
// formula failed, each with its code. Transitions and rules arrive with
// their phases.
type Preview struct {
	Document *Document `json:"document"`
	Errors   []*Error  `json:"errors,omitempty"`
}

// Option configures one call.
type Option func(*options)

type options struct {
	principal       Principal
	key             string
	businessDate    time.Time
	hasBusinessDate bool
	id              string
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

func apply(opts []Option) options {
	var o options
	for _, f := range opts {
		f(&o)
	}
	return o
}
