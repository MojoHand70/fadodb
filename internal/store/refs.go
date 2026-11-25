package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// checkRefs refuses a commit that sets a ref to a document which is not
// there, or which the caller may not read (ADR-0076, item 11). The
// answer is unknown_document either way, in the words get uses, so that
// setting a pointer tells nobody what exists. Only a pointer this commit
// sets to another document than it held is asked about: one the document
// held already is not checked again when other values change. A write of
// the engine's own is asked only whether the document is there, since
// no caller reads through it. In a preview the refusal is recorded and
// the commit goes on.
func (s *Store) checkRefs(ctx context.Context, c *class, cmd *command, before, after *docRow, dirty []bool, kids map[*child][]childRow, evalCtx *formula.Context, res *result) *Error {
	if len(c.refs) == 0 && !hasRefs(kids) {
		return nil
	}
	tell := func(e *Error) *Error {
		if e == nil || !cmd.preview {
			return e
		}
		res.errs = append(res.errs, e)
		return nil
	}
	for _, rl := range c.refs {
		if !dirty[rl.cv.idx] {
			continue
		}
		v := after.vals[rl.leaf]
		if v.IsBlank() || before != nil && beforeHeld(before, after, rl.leaf, v) {
			continue
		}
		as, e := s.pointsAtOne(ctx, cmd, c.leaves[rl.leaf].path, v.Str(), evalCtx)
		if e := tell(e); e != nil {
			return e
		}
		if as != "" {
			after.vals[rl.leaf] = formula.Ref(as)
		}
	}
	for ch, rows := range kids {
		if len(ch.refs) == 0 || !dirty[ch.idx] {
			continue
		}
		for i, r := range rows {
			for _, li := range ch.refs {
				if r[li].IsBlank() {
					continue
				}
				path := fmt.Sprintf("%s[%d]", ch.v.Name, i)
				if p := ch.leaves[li].path; p != "" && p != "value" {
					path += "." + p
				}
				as, e := s.pointsAtOne(ctx, cmd, path, r[li].Str(), evalCtx)
				if e := tell(e); e != nil {
					return e
				}
				if as != "" {
					r[li] = formula.Ref(as)
				}
			}
		}
	}
	return nil
}

// hasRefs reports whether a composite the commit was given has a leaf
// that points at a document.
func hasRefs(kids map[*child][]childRow) bool {
	for ch := range kids {
		if len(ch.refs) > 0 {
			return true
		}
	}
	return false
}

// beforeHeld reports whether the row held the same pointer before the
// commit, in the same layout.
func beforeHeld(before, after *docRow, leaf int, v formula.Value) bool {
	if before.classVersion != after.classVersion || leaf >= len(before.vals) {
		return false
	}
	was := before.vals[leaf]
	return !was.IsBlank() && was.Str() == v.Str()
}

// pointsAtOne answers whether the pointer names a document that is there
// and that the caller of the command may read. as is how the pointer is
// kept when the caller wrote the class in another case than its
// definition does, and "" when it is kept as written: one document has
// one pointer, which REF() and a list of what points at it compare with.
func (s *Store) pointsAtOne(ctx context.Context, cmd *command, variable, ref string, evalCtx *formula.Context) (as string, refused *Error) {
	name, id, _ := strings.Cut(ref, ":")
	gone := func() (string, *Error) {
		return "", errf(CodeUnknownDocument, variable, "no document %s of class %s", id, name)
	}
	t, e := s.class(name)
	if e != nil || t.w.selectCurrent == nil {
		return gone()
	}
	if name != t.cls.Name {
		as = t.cls.Name + ":" + id
	}
	// The row is read without who made it unless a formula of the class
	// pointed at asks for that, as a listing of transitions reads it.
	read := t.w.selectLean
	if t.who {
		read = t.w.selectCurrent
	}
	row, err := t.scanW(read.QueryRowContext(ctx, id), !t.who)
	if err != nil {
		if err == sql.ErrNoRows {
			return gone()
		}
		return "", &Error{Code: CodeStorage, Variable: variable, Message: "read the document the ref points at: " + err.Error()}
	}
	if cmd.engine || t.brought && cmd.o.principal.content() {
		return as, nil
	}
	// The read rule of the class pointed at, over that document, for this
	// caller: a document they may not read is not there for them.
	seen := *evalCtx
	seen.State, seen.ID, seen.Doc = row.state, row.id, nil
	if ok, _ := s.wev.may(t, ruleRead, row, &seen); !ok {
		return gone()
	}
	return as, nil
}
