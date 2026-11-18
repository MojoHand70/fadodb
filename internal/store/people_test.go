package store

import "github.com/MojoHand70/fadodb/internal/model"

// withoutDescriptions drops the notes of a missing description (ADR-0068,
// item 3), for a test about something else that defines a class without
// them.
func withoutDescriptions(ds model.Diagnostics) model.Diagnostics {
	var out model.Diagnostics
	for _, d := range ds {
		if d.Code != model.CodeNoDescription {
			out = append(out, d)
		}
	}
	return out
}
