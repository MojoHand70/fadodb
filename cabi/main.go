// Command cabi is FadoDB as a library of C (ADR-0101): the engine of
// fadodb-core, without the analytical engine, built with
//
//	go build -buildmode=c-shared -o libfadodb-core.so .
//
// into a shared library that a program of any language loads, as
// fadodb.h declares it. A program opens a database file with fado_open,
// sends it the commands of the command set (ADR-0055) with fado_exec,
// each a name and one JSON object answered with one JSON object, and
// closes it with fado_close. The commands and their answers are those of
// the server, byte for byte; only who calls is said beside the command,
// by the program, as a Go program says it with a Principal.
//
// It needs cgo and a C compiler, which is why it is a module of its own
// (ADR-0002), as the module analytics is.
package main

/*
#include <stdint.h>
#include <stdlib.h>
*/
import "C"

import "unsafe"

// main is never run: a library has no program of its own, and Go asks
// for the function all the same.
func main() {}

// text is a string of C as Go has it: nothing for NULL.
func text(s *C.char) string {
	if s == nil {
		return ""
	}
	return C.GoString(s)
}

// answer hands bytes to C, in memory that fado_free gives back.
func answer(b []byte) *C.char { return C.CString(string(b)) }

//export fado_open
func fado_open(path, options *C.char, refusal **C.char) C.uint64_t {
	n, refused := openDatabase(text(path), text(options))
	if refusal != nil {
		*refusal = nil
		if refused != nil {
			*refusal = answer(refused)
		}
	}
	return C.uint64_t(n)
}

//export fado_exec
func fado_exec(db C.uint64_t, caller, command, object *C.char) *C.char {
	return answer(execCommand(uint64(db), text(caller), text(command), text(object)))
}

//export fado_close
func fado_close(db C.uint64_t, refusal **C.char) C.int {
	refused := closeDatabase(uint64(db))
	if refusal != nil {
		*refusal = nil
		if refused != nil {
			*refusal = answer(refused)
		}
	}
	if refused != nil {
		return -1
	}
	return 0
}

//export fado_free
func fado_free(s *C.char) { C.free(unsafe.Pointer(s)) }

// versionText is made once and never given back: fado_version answers
// the same memory every time.
var versionText *C.char

//export fado_version
func fado_version() *C.char {
	if versionText == nil {
		versionText = C.CString(version)
	}
	return versionText
}
