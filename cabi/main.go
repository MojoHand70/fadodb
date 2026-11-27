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
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

typedef void (*fado_action)(void *context, uint64_t call, const char *action, const char *object);
*/
import "C"

import (
	"unsafe"

	// The library carries the database of time zones, as both programs
	// do (ADR-0093, item 3): a machine may have none, Windows never has
	// one, and a database whose zone is Europe/Warsaw would not open.
	_ "time/tzdata"
)

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
	return fado_open_with(path, options, nil, nil, refusal)
}

//export fado_open_with
func fado_open_with(path, options *C.char, actions C.fado_action, context unsafe.Pointer, refusal **C.char) C.uint64_t {
	n, refused := openDatabase(text(path), text(options), hostOf(actions, context))
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

//export fado_answer
func fado_answer(call C.uint64_t, answer *C.char) C.int {
	return C.int(answerCall(uint64(call), text(answer)))
}

//export fado_put_file
func fado_put_file(db C.uint64_t, caller, object *C.char, bytes unsafe.Pointer, length C.size_t) *C.char {
	var data []byte
	if bytes != nil && length > 0 {
		// The program's memory, read while the call runs and not kept.
		data = unsafe.Slice((*byte)(bytes), int(length))
	}
	return answer(putFile(uint64(db), text(caller), text(object), data))
}

//export fado_get_file
func fado_get_file(db C.uint64_t, caller, object *C.char, bytes *unsafe.Pointer, length *C.size_t) *C.char {
	a, data := getFile(uint64(db), text(caller), text(object))
	hand(data, bytes, length)
	return answer(a)
}

//export fado_get_blob
func fado_get_blob(db C.uint64_t, caller, object *C.char, bytes *unsafe.Pointer, length *C.size_t) *C.char {
	a, data := getBlob(uint64(db), text(caller), text(object))
	hand(data, bytes, length)
	return answer(a)
}

// hand gives the bytes of a file to C, in memory that fado_free gives
// back: never NULL for a file that was read, even an empty one, and
// NULL for one that was not.
func hand(data []byte, bytes *unsafe.Pointer, length *C.size_t) {
	if bytes != nil {
		*bytes = nil
		if data != nil {
			if len(data) == 0 {
				*bytes = C.malloc(1)
			} else {
				*bytes = C.CBytes(data)
			}
		}
	}
	if length != nil {
		*length = C.size_t(len(data))
	}
}

//export fado_free
func fado_free(p unsafe.Pointer) { C.free(p) }

// versionText is made once, when the library is loaded, and never given
// back: fado_version answers the same memory every time, from any
// thread.
var versionText *C.char

func init() { versionText = C.CString(version) }

//export fado_version
func fado_version() *C.char { return versionText }
