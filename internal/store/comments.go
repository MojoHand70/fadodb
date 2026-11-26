package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// EventCommented is the event of a comment (ADR-0098, item 8): its id is
// the comment's, its before_version the version the comment was written
// on, and it has no after_version, since a comment makes no version.
const EventCommented = "commented"

// Comment is one comment on a document (ADR-0098): the words of whoever
// wrote them, kept by the engine beside the document. Who and when are
// the engine's; the caller says only the words.
type Comment struct {
	// ID is the comment's, and the id of its event.
	ID    string `json:"comment_id"`
	Class string `json:"class"`
	DocID string `json:"doc_id"`
	Text  string `json:"text"`
	// ActorID is who wrote it, ActorKind what they are, person, agent or
	// service, and ActorFor whom they acted for (ADR-0050, ADR-0053).
	ActorID   string `json:"actor_id"`
	ActorKind string `json:"actor_kind,omitempty"`
	ActorFor  string `json:"actor_for,omitempty"`
	// Version is the version of the document the comment was written on.
	Version      int64  `json:"version"`
	BusinessDate string `json:"business_date"`
	TS           int64  `json:"ts"`
}

// commentsDDL is the table of comments (ADR-0098, item 11): made when a
// database is opened, an older one too, with no change to the schema's
// number.
var commentsDDL = []string{
	`CREATE TABLE IF NOT EXISTS fado_comments (comment_id TEXT PRIMARY KEY, class TEXT NOT NULL, doc_id TEXT NOT NULL, text TEXT NOT NULL, actor_id TEXT NOT NULL, actor_kind TEXT, actor_for TEXT, version INTEGER NOT NULL, business_date INTEGER NOT NULL, ts INTEGER NOT NULL)`,
	`CREATE INDEX IF NOT EXISTS fado_comments_doc ON fado_comments (class, doc_id, ts, comment_id)`,
}

// commentWords is the text of a comment as it is kept (ADR-0098, item
// 6): a carriage return before a line break is dropped, line breaks and
// tabs are kept, any other control character is refused; words that are
// empty once trimmed are refused, and so are words longer than the
// database's limit for a text.
func commentWords(text string, max int) (string, *Error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	for i, r := range text {
		if r == '\n' || r == '\t' {
			continue
		}
		if unicode.IsControl(r) {
			return "", &Error{Code: CodeBadValue, Message: "the words of a comment hold a control character at byte " + strconv.Itoa(i), Hint: "write plain text; a line break is kept", Path: "/text"}
		}
	}
	if strings.TrimSpace(text) == "" {
		return "", &Error{Code: CodeRequired, Message: "a comment needs words", Hint: "give the words in text", Path: "/text"}
	}
	if n := utf8.RuneCountInString(text); max > 0 && n > max {
		return "", &Error{Code: CodeOutOfRange, Message: fmt.Sprintf("the comment has %d code points; the database's limit for a text is %d (ADR-0021)", n, max), Hint: "write it shorter, or in several comments", Path: "/text"}
	}
	return text, nil
}

// commentHash identifies a comment's command for the key check of
// ADR-0026: the class, the document and the words.
func commentHash(class, id, text string) string {
	h := sha256.New()
	fmt.Fprintf(h, "comment\x00%s\x00%s\x00%s", strings.ToUpper(class), id, text)
	return hex.EncodeToString(h.Sum(nil))
}

// Comment writes a comment on a document (ADR-0098). Whoever may read
// the document as it is stored now may comment on it, in any state; the
// comment makes no version of it and runs nothing, and its event,
// commented, is written with it.
func (s *Store) Comment(ctx context.Context, class, id, text string, opts ...Option) (*Comment, error) {
	c, e := s.class(class)
	if e != nil {
		return nil, e
	}
	o, e := s.asked(ctx, opts, c.cls.Name)
	if e != nil {
		return nil, e
	}
	if e := refuseAt(o, "comment"); e != nil {
		return nil, e
	}
	if e := checkPrincipal(o.principal); e != nil {
		return nil, e
	}
	if strings.HasPrefix(o.key, outboxKeyPrefix) || strings.HasPrefix(o.key, inletKeyPrefix) {
		return nil, hintf(CodeBadValue, "", "a key may not start with "+outboxKeyPrefix+" or "+inletKeyPrefix+", the prefixes of the commits the engine makes", "choose another key")
	}
	if c.cls.States == nil {
		return nil, hintf(CodeStateless, "", c.cls.Name+" declares no states: it is a stateless application and stores no documents to comment on", "comment on a document of a class with states")
	}
	words, e := commentWords(text, s.limits.MaxText)
	if e != nil {
		return nil, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errf(CodeClosed, "", "the store is closed")
	}
	now := s.now()
	if _, err := s.writer.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return nil, storageErr("begin", err)
	}
	made, again, err := s.comment(ctx, c, id, words, o, now)
	if err != nil || again {
		if _, rerr := s.writer.ExecContext(context.WithoutCancel(ctx), "ROLLBACK"); rerr != nil && err == nil {
			return nil, storageErr("rollback", rerr)
		}
		return made, err
	}
	if err := s.endTransaction(ctx); err != nil {
		_, _ = s.writer.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
		return nil, storageErr("commit", err)
	}
	return made, nil
}

// comment is the work of Comment inside the writer's transaction. again
// is true for a key that wrote this comment before, which is answered as
// it was and writes nothing (ADR-0098, item 9).
func (s *Store) comment(ctx context.Context, c *class, id, words string, o options, now time.Time) (*Comment, bool, error) {
	hash := ""
	if o.key != "" {
		hash = commentHash(c.cls.Name, id, words)
		var kind, class, docID, keyed string
		var after sql.NullInt64
		var stored sql.NullString
		err := s.stmts.lookupKey.QueryRowContext(ctx, o.key).Scan(&kind, &class, &docID, &after, &stored, &keyed)
		switch {
		case err == sql.ErrNoRows:
		case err != nil:
			return nil, false, storageErr("look up the command key", err)
		case kind != EventCommented || stored.String != hash:
			return nil, false, hintf(CodeKeyReused, "", "the key "+o.key+" was used by a different command (ADR-0026)", "a key names one command; a new command takes a new key")
		default:
			made, err := s.commentByID(ctx, keyed)
			return made, true, err
		}
	}
	// The document as it is stored now, under the class version that
	// computed it: a deleted one takes no comment, and one the caller may
	// not see is not there for them (ADR-0049, item 3).
	row, err := c.scanW(c.w.selectCurrent.QueryRowContext(ctx, id), false)
	if err == sql.ErrNoRows {
		return nil, false, noDocument(c, id)
	}
	if err != nil {
		return nil, false, storageErr("read the document", err)
	}
	vc := c
	if row.classVersion != c.cls.Version && row.classVersion != 0 {
		if vc = s.classAt(c.cls.Name, row.classVersion); vc == nil {
			return nil, false, errf(CodeStorage, "", "the document %s was computed by version %d of %s, which the store does not have", row.id, row.classVersion, c.cls.Name)
		}
		if row, err = vc.scanW(vc.w.selectCurrent.QueryRowContext(ctx, id), false); err != nil {
			return nil, false, storageErr("read the document under its class version", err)
		}
	}
	if !s.seesRow(c, vc, row, o) {
		return nil, false, noDocument(c, id)
	}
	if o.version != 0 && o.version != row.version {
		return nil, false, staleVersion(c, row, o.version)
	}
	s.actAs(o.principal)
	secs, day := now.Unix(), businessDay(o, now)
	made := &Comment{ID: s.ids.next(now), Class: c.cls.Name, DocID: row.id, Text: words, ActorID: o.principal.ID, ActorKind: o.principal.Kind, ActorFor: o.principal.For,
		Version: row.version, BusinessDate: formula.Render(formula.Date(day)), TS: secs}
	if err := s.writeEvent(ctx, made.ID, made.Class, made.DocID, eventInfo{kind: EventCommented}, made.ActorID, row.version, nil, o.key, hash, day, secs); err != nil {
		return nil, false, err
	}
	if _, err := s.writer.ExecContext(ctx, "INSERT INTO fado_comments (comment_id, class, doc_id, text, actor_id, actor_kind, actor_for, version, business_date, ts) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		made.ID, made.Class, made.DocID, made.Text, made.ActorID, nullText(made.ActorKind), nullText(made.ActorFor), made.Version, day, secs); err != nil {
		return nil, false, storageErr("keep the comment", err)
	}
	return made, false, nil
}

// commentByID reads one comment on the writer, for the repeat of a key.
func (s *Store) commentByID(ctx context.Context, commentID string) (*Comment, error) {
	rows, err := s.writer.QueryContext(ctx, commentsSQL+" WHERE comment_id = ?", commentID)
	if err != nil {
		return nil, storageErr("read the comment", err)
	}
	list, err := scanComments(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, errf(CodeStorage, "", "the key names the comment %s, which the database does not hold", commentID)
	}
	return list[0], nil
}

const commentsSQL = "SELECT comment_id, class, doc_id, text, actor_id, actor_kind, actor_for, version, business_date, ts FROM fado_comments"

func scanComments(rows *sql.Rows) ([]*Comment, error) {
	defer rows.Close()
	var out []*Comment
	for rows.Next() {
		var cm Comment
		var kind, acts sql.NullString
		var day int64
		if err := rows.Scan(&cm.ID, &cm.Class, &cm.DocID, &cm.Text, &cm.ActorID, &kind, &acts, &cm.Version, &day, &cm.TS); err != nil {
			return nil, storageErr("read the comments", err)
		}
		cm.ActorKind, cm.ActorFor = kind.String, acts.String
		cm.BusinessDate = formula.Render(formula.Date(day))
		out = append(out, &cm)
	}
	if err := rows.Err(); err != nil {
		return nil, storageErr("read the comments", err)
	}
	return out, nil
}

// Comments reads the comments on a document, the oldest first; none for
// a document the principal may not see, as Events answers (ADR-0098,
// item 3). A content administrator reads past the rule with a reason,
// which the log of reads keeps.
func (s *Store) Comments(ctx context.Context, class, id string, opts ...Option) ([]*Comment, error) {
	c, e := s.class(class)
	if e != nil {
		return nil, e
	}
	o, e := s.asked(ctx, opts, c.cls.Name)
	if e != nil {
		return nil, e
	}
	r := o.newRead("comments", c)
	if seen, err := s.lookID(ctx, c, id, o, r); err != nil {
		return nil, err
	} else if !seen {
		return nil, nil
	}
	q := commentsSQL + " WHERE class = ? AND doc_id = ?"
	args := []any{c.cls.Name, id}
	if !o.at.IsZero() { // what was written up to the moment the call reads at
		q += " AND ts <= ?"
		args = append(args, o.at.Unix())
	}
	rows, err := s.db.QueryContext(ctx, q+" ORDER BY ts, comment_id", args...)
	if err != nil {
		return nil, storageErr("read the comments", err)
	}
	out, err := scanComments(rows)
	if err != nil {
		return nil, err
	}
	if err := s.logRead(ctx, o, r); err != nil {
		return nil, err
	}
	return out, nil
}
