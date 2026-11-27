// Package secret finds the secrets of the connections of a database
// (ADR-0059, item 5), for fadodb serve and for the library of C
// (ADR-0101): a secret is never in a definition, in the settings or on
// a command line. The secret token of the connection slack is in one of
// two places:
//
//   - the file slack/token in the directory of secrets, when there is
//     one;
//   - the environment, as FADODB_SECRET_SLACK__TOKEN.
//
// The file is looked for first. Both are read again for every request,
// so a secret is changed without a restart.
package secret

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/MojoHand70/fadodb/internal/model"
)

// Finder finds secrets in a directory and the environment.
type Finder struct {
	// Dir is the directory of secrets, "" when none was given.
	Dir string
	// Env reads a variable of the environment; os.LookupEnv.
	Env func(string) (string, bool)
	// Holder is who looks, as the words of an error say it: the
	// server, or the program that loaded the library.
	Holder string
}

// Max is the longest secret read from a file.
const Max = 64 << 10

// File is where the secret of a connection is in the directory of
// secrets: the connection's name as a directory and the secret's as a
// file, both in small letters, since names differ in case nowhere in
// the engine.
func File(connection, name string) string {
	return strings.ToLower(connection) + "/" + strings.ToLower(name)
}

// Variable is the variable of the environment that holds the
// secret of a connection. The two names are joined by two underscores,
// which no name holds (ADR-0031), so the variable of one connection is
// never the variable of another: with one underscore, the secret c of a
// connection a_b and the secret b_c of a connection a would be one
// variable, and a definition could read the first to send it to the
// hosts of the second.
func Variable(connection, name string) string {
	return "FADODB_SECRET_" + strings.ToUpper(connection) + "__" + strings.ToUpper(name)
}

// Find answers the secret of a connection, as fado.Options.Secrets asks
// it at every request. The error is why there is none, in words that
// name no directory of the machine and never a value: the engine keeps
// them with the action that was not sent.
func (f *Finder) Find(_ context.Context, connection, name string) (string, error) {
	// The engine asks for the names of a definition, which are names. A
	// name that is none is never made a path.
	if model.NameReason(connection) != "" || model.NameReason(name) != "" {
		return "", errors.New("it has no name a secret is kept under")
	}
	file, variable := File(connection, name), Variable(connection, name)
	if f.Dir != "" {
		value, err := f.read(connection, name)
		switch {
		case err == nil && value == "":
			return "", fmt.Errorf("the file %s of the %s's secrets directory is empty", file, f.Holder)
		case err == nil:
			return value, nil
		case !errors.Is(err, fs.ErrNotExist):
			return "", fmt.Errorf("the file %s of the %s's secrets directory cannot be read: %v", file, f.Holder, err)
		}
	}
	if value, set := f.Env(variable); set && value != "" {
		return value, nil
	}
	if f.Dir == "" {
		return "", fmt.Errorf("the %s's environment has no %s, and the %s was given no secrets directory, where it would be the file %s", f.Holder, variable, f.Holder, file)
	}
	return "", fmt.Errorf("the %s's secrets directory has no file %s, and its environment no %s", f.Holder, file, variable)
}

// read reads the file of a secret, without the end of line a file is
// usually written with.
func (f *Finder) read(connection, name string) (string, error) {
	file, err := os.Open(filepath.Join(f.Dir, strings.ToLower(connection), strings.ToLower(name)))
	if err != nil {
		return "", plain(err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, Max+1))
	if err != nil {
		return "", plain(err)
	}
	if len(data) > Max {
		return "", fmt.Errorf("it is longer than %d bytes", Max)
	}
	return strings.TrimRight(string(data), "\r\n"), nil
}

// plain is the error of a file without its path: where the secrets are
// on the machine is the operator's to know.
func plain(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}
