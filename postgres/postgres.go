package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// Open opens the Postgres database at the address, a URL as
// postgres://user:password@host:port/database or the key=value form
// Postgres's own programs take. A schema, when one is given, is where
// the connections look for their tables and functions and make them:
// their search path is that schema alone. No connection is made until
// the handle is first used, as with sql.Open.
func Open(address, schema string) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(address)
	if err != nil {
		return nil, fmt.Errorf("postgres: the address: %w", err)
	}
	if schema != "" {
		cfg.RuntimeParams["search_path"] = quoteIdent(schema)
	}
	return sql.OpenDB(connector{stdlib.GetConnector(*cfg)}), nil
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// connector makes the connections of a handle: pgx's, each behind the
// spelling of parameters.
type connector struct{ driver.Connector }

func (c connector) Connect(ctx context.Context) (driver.Conn, error) {
	inner, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &conn{inner}, nil
}

func (c connector) Driver() driver.Driver { return c.Connector.Driver() }

// conn is one connection. What database/sql asks of a connection
// beyond the three methods every driver has, it asks by a type
// assertion, so each of those is passed on by name.
type conn struct{ driver.Conn }

func (c *conn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	s := spell(query)
	inner, err := c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, s.text)
	if err != nil {
		return nil, err
	}
	return &stmt{Stmt: inner, s: s}, nil
}

func (c *conn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}

func (c *conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	s := spell(query)
	args, err := s.order(args)
	if err != nil {
		return nil, err
	}
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, s.text, args)
}

func (c *conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	s := spell(query)
	args, err := s.order(args)
	if err != nil {
		return nil, err
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, s.text, args)
}

func (c *conn) BeginTx(ctx context.Context, o driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, o)
}

func (c *conn) Ping(ctx context.Context) error { return c.Conn.(driver.Pinger).Ping(ctx) }

// CheckNamedValue takes every argument as it is, a named one too: pgx
// converts the values, and order has put them in their places.
func (c *conn) CheckNamedValue(*driver.NamedValue) error { return nil }

func (c *conn) ResetSession(ctx context.Context) error {
	if r, ok := c.Conn.(driver.SessionResetter); ok {
		return r.ResetSession(ctx)
	}
	return nil
}

func (c *conn) IsValid() bool {
	if v, ok := c.Conn.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}

// stmt is a prepared statement: pgx's, with the order its arguments
// go in.
type stmt struct {
	driver.Stmt
	s *spelled
}

// NumInput says the number of arguments is not known beforehand: a
// named argument the statement does not use may be given, and is left
// out.
func (s *stmt) NumInput() int { return -1 }

func (s *stmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	args, err := s.s.order(args)
	if err != nil {
		return nil, err
	}
	return s.Stmt.(driver.StmtExecContext).ExecContext(ctx, args)
}

func (s *stmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	args, err := s.s.order(args)
	if err != nil {
		return nil, err
	}
	return s.Stmt.(driver.StmtQueryContext).QueryContext(ctx, args)
}
