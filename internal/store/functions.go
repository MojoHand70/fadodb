package store

import "github.com/MojoHand70/fadodb/internal/formula"

// FunctionCatalogue is the engine's list of the functions a formula can
// call, as it is told to a caller (ADR-0074): each a FunctionInfo, and
// the kinds of function, each a FunctionKind, with what a kind means for
// where a function may stand.
type (
	FunctionCatalogue = formula.Told
	FunctionInfo      = formula.ToldFunction
	FunctionKind      = formula.ToldKind
	// FunctionParam is one argument of a function.
	FunctionParam = formula.Param
)

// Functions answers the engine's list of functions (ADR-0074). It is the
// engine's and the same for every database: the list the parser reads
// and the manual is written from, so a program that helps somebody write
// a formula needs no copy of its own.
func (s *Store) Functions() *FunctionCatalogue { return formula.Tell() }
