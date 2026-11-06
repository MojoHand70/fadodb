package formula

// Limits are the size limits a database evaluates under (ADR-0021). They
// are a property of the database, chosen when it is created and kept with
// its metadata, so that a formula gives the same result wherever the
// database is opened and a commit recomputes as it was committed. A
// server's resource budgets, memory, threads and timeouts, are not limits:
// they decide whether a recompute runs, never what it returns.
type Limits struct {
	// MaxText is the longest text a formula may produce or a document may
	// store, in code points. A longer result is the error out_of_range,
	// decided before the text is built.
	MaxText int
}

// DefaultMaxText is the text limit of a new database: 65,536 code points,
// room for a long note, and small enough that a vector of 2,048 of them
// on the analytical engine stays under a gigabyte.
const DefaultMaxText = 1 << 16

// MinMaxText is the smallest text limit a database may set: every
// rendering of a number or a date fits in it.
const MinMaxText = 64

// DefaultLimits are the limits of a new database.
func DefaultLimits() Limits { return Limits{MaxText: DefaultMaxText} }

// maxText is the text limit the VM evaluates under, the default for a nil
// VM, which the SQL dialects' registered functions pass when the function
// needs no limit.
func (vm *VM) maxText() int {
	if vm == nil || vm.Limits.MaxText <= 0 {
		return DefaultMaxText
	}
	return vm.Limits.MaxText
}
