// Package sqlitefn registers the formula engine's Go functions into the
// pure-Go SQLite driver, so that the SQLite dialect of the SQL generator
// agrees with the Go VM by construction (ADR-0019). It is the only package
// of the formula engine that imports the driver; the core stays free of
// it and the generator itself is plain Go.
package sqlitefn

import (
	"database/sql/driver"
	"sync"

	"modernc.org/sqlite"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// DriverName is the database/sql driver name the registration applies to.
const DriverName = "sqlite"

var (
	once   sync.Once
	regErr error
)

// Register registers every function the SQLite dialect expects, once per
// process. It must run before a connection that uses them is opened.
func Register() error {
	once.Do(func() {
		for _, f := range formula.SQLiteFunctions() {
			if regErr = register(f); regErr != nil {
				return
			}
		}
	})
	return regErr
}

func register(f formula.RegisteredFunc) error {
	convert := func(args []driver.Value) []formula.Value {
		vals := make([]formula.Value, len(args))
		for i, a := range args {
			vals[i] = formula.ValueFromDriver(a, f.Kind(i))
		}
		return vals
	}
	value := func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		v, err := f.Fn(nil, convert(args))
		if err != nil {
			return nil, nil
		}
		return v.DriverValue(), nil
	}
	code := func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		if _, err := f.Fn(nil, convert(args)); err != nil {
			return err.Code, nil
		}
		return nil, nil
	}
	if err := sqlite.RegisterDeterministicScalarFunction(f.Name, int32(f.NArgs), value); err != nil {
		return err
	}
	return sqlite.RegisterDeterministicScalarFunction(f.Name+"_err", int32(f.NArgs), code)
}
