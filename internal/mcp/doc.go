// Package mcp is the door for agents (ADR-0064): it offers the commands
// of a database as tools, in the Model Context Protocol that an agent's
// program speaks.
//
// A tool is a command. Its name is the command's, its arguments are the
// command's object and its answer is the command's answer, all written
// from the table of the commands (ADR-0055, item 2), so that a command
// added to the table is a tool with no work here. A command the database
// refuses is answered with its refusal as the database wrote it, marked
// as an error, so that an agent reads the code and the hint.
//
// The door holds no database and judges nothing. Whoever makes it gives
// it the function that carries a command to a database and brings the
// answer back; who the caller is belongs to that function, and no tool
// lets an agent say it.
//
// The door speaks both forms of the protocol: the revision of
// 2026-07-28, in which every request says its version, and the
// revisions before it, which open with a greeting. Serve reads the
// messages of one agent's program, a message to a line, and writes the
// answers the same way.
//
// The package has no variable, so two doors in one program share
// nothing.
package mcp
