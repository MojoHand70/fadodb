// Package server is the HTTP door of a database: it turns a request
// into a command of the command set, runs it and writes the answer
// (ADR-0055, items 8 and 10).
//
// A door serves one database (ADR-0056, item 4). It is an http.Handler
// that a program mounts where it listens; it does not listen itself.
// Whoever makes it gives it the function that says who sends a request
// (ADR-0027, ADR-0057), and the door asks nothing else about the caller.
// Its limits, the largest request, the time one command may take and how
// many run at once, are its own and count for its database only.
//
// The door keeps nothing outside itself: the package has no variable,
// so two doors in one program share nothing, and a test holds the
// package to it.
//
// A host program reaches the door through the package fado/httpdoor.
// What is not here yet, each with its step: the stream of changes, the
// files beside the commands, the engine's own keys, and the program
// `fadodb serve`.
package server
