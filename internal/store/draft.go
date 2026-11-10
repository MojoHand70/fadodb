package store

import (
	"context"
	"database/sql"
	"strings"

	"github.com/MojoHand70/fadodb/internal/model"
)

// PreviewDraft compiles a class definition for this request alone and
// runs a create (empty id) or a set of an existing document through the
// commit pipeline under it, writing nothing and storing nothing
// (ADR-0025): a designer tries a draft on the company's own documents.
// The document is read from the stored class of the draft's name, under
// the version that computed it, and recomputed in full under the draft;
// a terminal document may be looked at this way, since nothing changes.
// A draft that does not compile returns its Diagnostics.
func (s *Store) PreviewDraft(ctx context.Context, definition []byte, id string, values Values, transition string, opts ...Option) (*Preview, error) {
	if transition != "" && id == "" {
		return nil, hintf(CodeBadValue, "", "a transition moves an existing document; no id was given", "name a document of the stored class, or preview the create alone")
	}
	compact, err := compactJSON(definition)
	if err != nil {
		return nil, model.Diagnostics{{Code: model.CodeBadJSON, Pos: -1, Message: err.Error()}}
	}
	s.cmu.RLock()
	cls, diags := model.DefineIn(definition, s.env())
	var stored *class
	if cls != nil {
		stored = s.classes[strings.ToUpper(cls.Name)]
	}
	s.cmu.RUnlock()
	if len(diags) > 0 {
		return nil, diags
	}
	if stored != nil {
		cls.Version = stored.cls.Version + 1
	} else if id != "" {
		return nil, errf(CodeUnknownClass, "", "no stored class %q to read the document %s from", cls.Name, id)
	}
	draft, err := newClass(cls, compact)
	if err != nil {
		return nil, err
	}
	o := apply(opts)
	cmd := command{op: opSet, c: draft, stored: stored, id: id, transition: transition, values: values, o: o, preview: true}
	if transition != "" {
		cmd.op = opTransition
	}
	if id == "" {
		// A create under the draft: nothing is read, so the stored class
		// is not needed, and the draft has no statements of its own.
		cmd = command{op: opCreate, c: draft, values: values, o: o, preview: true}
	}
	res, err := s.commit(ctx, cmd)
	s.dropPlansOf(draft)
	if err != nil {
		return nil, err
	}
	return &Preview{Document: res.doc, Errors: res.errs, Path: res.path, Transitions: res.transitions, Rules: res.rules, Now: res.now}, nil
}

// dropPlansOf forgets the lookup plans made for a draft's programs, with
// their statements.
func (s *Store) dropPlansOf(c *class) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for prog := range c.slots {
		for i := range prog.Queries {
			if p, ok := s.plans[&prog.Queries[i]]; ok {
				for _, st := range []*sql.Stmt{p.stmt, p.keyed} {
					if st != nil {
						st.Close()
					}
				}
				delete(s.plans, &prog.Queries[i])
			}
		}
	}
}

// draftChildren completes the composites of a document previewed under a
// draft: the ones given are kept, and for the others the stored class's
// rows are read where the draft has a composite of the same name and the
// same columns; a composite the draft reshaped shows empty.
func (s *Store) draftChildren(ctx context.Context, draft, stored *class, before *docRow, kids map[*child][]childRow) (map[*child][]childRow, error) {
	all := map[*child][]childRow{}
	for ch, rows := range kids {
		all[ch] = rows
	}
	for _, ch := range draft.children {
		if _, ok := all[ch]; ok {
			continue
		}
		prev, ok := stored.childOfName(ch.v.Name)
		if !ok || !sameLeaves(prev.leaves, ch.leaves) {
			all[ch] = nil
			continue
		}
		rows, err := prev.load(ctx, prev.selectRows, before.id, before.version)
		if err != nil {
			return nil, err
		}
		all[ch] = rows
	}
	return all, nil
}

func sameLeaves(a, b []leaf) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(a[i].path, b[i].path) || !a[i].typ.Equal(b[i].typ) {
			return false
		}
	}
	return true
}
