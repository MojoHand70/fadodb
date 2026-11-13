package store

import (
	"strconv"
	"time"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// Principal is who a call runs for (ADR-0027): the embedder's id for the
// actor, the groups INGROUP tests, the system groups the caller is in,
// and the embedder's business day, the default business date of a commit
// (ADR-0026). A zero Today means none.
type Principal struct {
	ID     string
	Groups []string
	// System lists the system groups the caller is in (ADR-0052), which
	// hold the work a class does not give: GroupAdministrators,
	// GroupDesigners, GroupContentAdministrators and GroupAnalysts. The
	// host program gives them here, apart from Groups, so that a group of
	// its own directory that happens to have one of these names gives
	// nothing; such a name among Groups is refused. INGROUP answers them
	// as it answers Groups, and answers GroupEveryone for every caller
	// that has an id.
	System []string
	Today  time.Time
	// Kind is what the caller is (ADR-0050): ActorPerson, ActorAgent or
	// ActorService, or empty when the host program does not say. For is
	// the id of whoever the caller acts for, empty when it acts for
	// itself. The engine trusts both as it trusts the id, writes them on
	// every event beside it, and answers them to USERKIND() and
	// ACTINGFOR().
	Kind string
	For  string
}

// What a caller is, as Principal.Kind says it (ADR-0050): a person who
// sent the command, an agent that decided to send it, or a service, a
// program that decides nothing, such as an import or a schedule.
const (
	ActorPerson  = "person"
	ActorAgent   = "agent"
	ActorService = "service"
)

// The system groups (ADR-0052). Four hold the work a class does not
// give, and the host program says who is in them, in Principal.System:
// a database administrator operates the outbox and the blobs, a designer
// defines, a content administrator sees every document and acts on it
// past the rules of its class, and an analyst runs SQL. The fifth names
// every caller that has an id, and the engine gives it by itself.
const (
	GroupAdministrators        = "administrators"
	GroupDesigners             = "designers"
	GroupContentAdministrators = "content-administrators"
	GroupAnalysts              = "analysts"
	GroupEveryone              = formula.GroupEveryone
)

// systemGroup reports whether the name is one of the four system groups
// a host program gives.
func systemGroup(name string) bool {
	switch name {
	case GroupAdministrators, GroupDesigners, GroupContentAdministrators, GroupAnalysts:
		return true
	}
	return false
}

// in reports whether the principal is in the system group.
func (p Principal) in(group string) bool {
	for _, g := range p.System {
		if g == group {
			return true
		}
	}
	return false
}

// content reports whether the principal is a content administrator, who
// sees every document and acts on it past the rules of its class
// (ADR-0052, item 4). A guard, the statechart and a required value hold
// for them as for everyone.
func (p Principal) content() bool { return p.in(GroupContentAdministrators) }

// checkPrincipal refuses a principal whose kind is none of the three,
// whose system groups name something else than the four a host gives,
// or whose own groups carry the name of a system group (ADR-0052, item
// 2).
func checkPrincipal(p Principal) *Error {
	switch p.Kind {
	case "", ActorPerson, ActorAgent, ActorService:
	default:
		return hintf(CodeBadValue, "", "a principal's kind is "+ActorPerson+", "+ActorAgent+" or "+ActorService+", got "+strconv.Quote(p.Kind),
			"leave the kind empty when the host program does not know what the caller is")
	}
	for _, g := range p.System {
		if systemGroup(g) {
			continue
		}
		hint := "the system groups are " + GroupAdministrators + ", " + GroupDesigners + ", " + GroupContentAdministrators + " and " + GroupAnalysts + "; any other group goes in Principal.Groups"
		if g == GroupEveryone {
			hint = "the engine puts every caller that has an id in " + GroupEveryone + " by itself; leave it out"
		}
		return hintf(CodeBadValue, "", strconv.Quote(g)+" is not a system group a host program gives", hint)
	}
	for _, g := range p.Groups {
		if systemGroup(g) || g == GroupEveryone {
			return hintf(CodeBadValue, "", "the group "+strconv.Quote(g)+" has the name of a system group",
				"give a system group in Principal.System; a group of the host program's own that has this name needs another name here")
		}
	}
	return nil
}

// asked applies the options of a call that reads, and refuses a principal
// whose kind is none the engine knows (ADR-0050); a command that writes
// is refused the same where it begins.
func asked(opts []Option) (options, *Error) {
	if len(opts) == 0 {
		// A call without options is nobody's: nothing to apply, and the
		// options stay off the heap, which Drain is glad of on every run.
		return options{}, nil
	}
	o := apply(opts)
	return o, checkPrincipal(o.principal)
}

// contextOf is the context of a formula that runs for the principal at
// the moment: who asks, in which groups, what they are and for whom they
// act.
func contextOf(p Principal, now int64) formula.Context {
	return formula.Context{User: p.ID, Groups: p.Groups, System: p.System, Kind: p.Kind, For: p.For, Now: now}
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
	CreatedAt      int64 `json:"created_at"`
	UpdatedAt      int64 `json:"updated_at"`
	StateEnteredAt int64 `json:"state_entered_at,omitempty"`
	// Who created the document, made this version and moved it into its
	// state, each the id of the principal of that command and empty when
	// it named none, and the state it was in before (ADR-0047, item 4).
	CreatedBy        string `json:"created_by,omitempty"`
	UpdatedBy        string `json:"updated_by,omitempty"`
	StateEnteredBy   string `json:"state_entered_by,omitempty"`
	StateEnteredFrom string `json:"state_entered_from,omitempty"`
	Values           Values `json:"values"`
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
	ID        string `json:"event_id"`
	Class     string `json:"class"`
	DocID     string `json:"doc_id"`
	Kind      string `json:"kind"`
	Name      string `json:"name,omitempty"`
	FromState string `json:"from_state,omitempty"`
	ToState   string `json:"to_state,omitempty"`
	ActorID   string `json:"actor_id,omitempty"`
	// ActorKind is what the actor was, person, agent or service, and
	// ActorFor the id it acted for (ADR-0050); each empty when the host
	// program did not say.
	ActorKind string `json:"actor_kind,omitempty"`
	ActorFor  string `json:"actor_for,omitempty"`
	// Override names the rules a content administrator's command got
	// past, the place of each in the class, separated by a space, as
	// "/access/update"; Reason is the reason the command gave (ADR-0052,
	// item 4). Both are empty for a command the rules let through.
	Override      string `json:"override,omitempty"`
	Reason        string `json:"reason,omitempty"`
	BeforeVersion int64  `json:"before_version,omitempty"`
	AfterVersion  int64  `json:"after_version,omitempty"`
	CauseEventID  string `json:"cause_event_id,omitempty"`
	CommandKey    string `json:"command_key,omitempty"`
	// BusinessDate is the calendar day as YYYY-MM-DD.
	BusinessDate string `json:"business_date"`
	TS           int64  `json:"ts"`
	// Code is the code an action died with, on an event of kind
	// action_failed (ADR-0044); empty otherwise.
	Code string `json:"code,omitempty"`
	// Origin says, on an event that enters a state, how the move came
	// about (ADR-0047, item 3): command, action, guard or timer.
	Origin string `json:"origin,omitempty"`
}

// The event kinds this build writes, and the qualifiers of their
// event_docs rows.
const (
	EventCreated    = "created"
	EventSet        = "set"
	EventDeleted    = "deleted"
	EventMigrated   = "migrated"
	EventTransition = "transition"
	// EventReset and EventRefresh are the events of the Reset and the
	// Refresh commands (ADR-0043).
	EventReset   = "reset"
	EventRefresh = "refresh"
	// EventActionRun and EventActionFailed are the outcome of an action of
	// the outbox (ADR-0044), on the document whose commit queued it: the
	// name is the action's and the cause the event of that commit. An
	// action that is retried has no event until it runs or dies.
	EventActionRun    = "action_run"
	EventActionFailed = "action_failed"
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
// the guard's error when it errored. A timed transition carries FiresAt,
// the moment its time comes in seconds since the epoch (ADR-0047, item
// 8), 0 when it never comes; once that is past, the next commit that
// touches the document fires it, if its guard holds. A timed transition
// without a name is listed for that moment alone: nobody takes it, so it
// is not enabled and has no code.
type TransitionInfo struct {
	Name    string `json:"name"`
	To      string `json:"to"`
	Enabled bool   `json:"enabled"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	FiresAt int64  `json:"fires_at,omitempty"`
	// Override is the place of the rule the caller would get past by
	// taking the transition as a content administrator, which needs a
	// reason (ADR-0052, item 4); empty when the rule lets them through.
	Override string `json:"override,omitempty"`
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
	// Override lists the places of the rules the command gets past by
	// the content administrator's group alone (ADR-0052, item 4). Such a
	// command needs a reason; without one the preview also reports
	// reason_required under Errors.
	Override []string `json:"override,omitempty"`
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
// arguments of a queued action arrive with the outbox. Into are the
// variables the result of a queued action will be written to when it has
// ended (ADR-0045, item 8); a preview runs nothing, so it has no result
// to show.
type ActionRun struct {
	Action string   `json:"action"`
	Queued bool     `json:"queued,omitempty"`
	Args   Values   `json:"args,omitempty"`
	Into   []string `json:"into,omitempty"`
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
	// reason is why a content administrator acts past a rule (ADR-0052,
	// item 4): the event of the commit keeps it.
	reason string
}

// As runs the call as the principal.
func As(p Principal) Option { return func(o *options) { o.principal = p } }

// Reason says why the caller acts, for a command that only a content
// administrator's group lets through (ADR-0052, item 4): such a command
// is an override of the rule that would have refused it, it is refused
// with reason_required without a reason, and the event of its commit
// keeps the reason beside the rule. A command the rules let through
// ignores it.
func Reason(text string) Option { return func(o *options) { o.reason = text } }

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
