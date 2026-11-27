package cli

import (
	"context"

	"github.com/MojoHand70/fadodb/internal/secret"
)

// secrets finds the secrets of the connections for the server (ADR-0059,
// item 5), as package secret does for every program that opens a
// database. A secret is never in the settings, in a definition or on the
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
const maxSecret = secret.Max

// secretFile is where the secret of a connection is in the directory of
// secrets, and secretVariable the variable of the environment that
// holds it.
func secretFile(connection, name string) string     { return secret.File(connection, name) }
func secretVariable(connection, name string) string { return secret.Variable(connection, name) }

// find answers the secret of a connection, as fado.Options.Secrets asks
// it at every request, in words that name no directory of the machine
// and never a value.
func (s *secrets) find(ctx context.Context, connection, name string) (string, error) {
	f := secret.Finder{Dir: s.dir, Env: s.env, Holder: "server"}
	return f.Find(ctx, connection, name)
}
