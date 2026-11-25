package cli

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

// secrets finds the secrets of the connections for the server (ADR-0059,
// item 5). A secret is never in the settings, in a definition or on the
// command line. The secret token of the connection slack is in one of
// two places:
//
//   - the file slack/token in the directory given with --secrets-dir;
//   - the environment, as FADODB_SECRET_SLACK__TOKEN.
//
// The file is looked for first. Both are read again for every request,
// so a secret is changed without a restart.
type secrets struct {
	// dir is the directory of secrets, "" when none was given.
	dir string
	// env reads a variable of the environment; os.LookupEnv.
	env func(string) (string, bool)
}

// maxSecret is the longest secret the server reads from a file.
const maxSecret = 64 << 10

// secretFile is where the secret of a connection is in the directory of
// secrets: the connection's name as a directory and the secret's as a
// file, both in small letters, since names differ in case nowhere in
// the engine.
func secretFile(connection, name string) string {
	return strings.ToLower(connection) + "/" + strings.ToLower(name)
}

// secretVariable is the variable of the environment that holds the
// secret of a connection. The two names are joined by two underscores,
// which no name holds (ADR-0031), so the variable of one connection is
// never the variable of another: with one underscore, the secret c of a
// connection a_b and the secret b_c of a connection a would be one
// variable, and a definition could read the first to send it to the
// hosts of the second.
func secretVariable(connection, name string) string {
	return "FADODB_SECRET_" + strings.ToUpper(connection) + "__" + strings.ToUpper(name)
}

// find answers the secret of a connection, as fado.Options.Secrets asks
// it at every request. The error is why there is none, in words that
// name no directory of the machine and never a value: the engine keeps
// them with the action that was not sent.
func (s *secrets) find(_ context.Context, connection, name string) (string, error) {
	// The engine asks for the names of a definition, which are names. A
	// name that is none is never made a path.
	if model.NameReason(connection) != "" || model.NameReason(name) != "" {
		return "", errors.New("it has no name a secret is kept under")
	}
	file, variable := secretFile(connection, name), secretVariable(connection, name)
	if s.dir != "" {
		value, err := s.read(connection, name)
		switch {
		case err == nil && value == "":
			return "", fmt.Errorf("the file %s of the server's secrets directory is empty", file)
		case err == nil:
			return value, nil
		case !errors.Is(err, fs.ErrNotExist):
			return "", fmt.Errorf("the file %s of the server's secrets directory cannot be read: %v", file, err)
		}
	}
	if value, set := s.env(variable); set && value != "" {
		return value, nil
	}
	if s.dir == "" {
		return "", fmt.Errorf("the server's environment has no %s, and the server was given no secrets directory, where it would be the file %s", variable, file)
	}
	return "", fmt.Errorf("the server's secrets directory has no file %s, and its environment no %s", file, variable)
}

// read reads the file of a secret, without the end of line a file is
// usually written with.
func (s *secrets) read(connection, name string) (string, error) {
	f, err := os.Open(filepath.Join(s.dir, strings.ToLower(connection), strings.ToLower(name)))
	if err != nil {
		return "", plain(err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxSecret+1))
	if err != nil {
		return "", plain(err)
	}
	if len(data) > maxSecret {
		return "", fmt.Errorf("it is longer than %d bytes", maxSecret)
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
