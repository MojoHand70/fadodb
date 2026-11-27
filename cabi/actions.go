package main

// The function of C the program carries out its actions with (ADR-0102):
// Go calls a function of C through a pointer only from C, so a small
// function of C here does it. It is in a file of its own because a file
// that exports functions may declare C and not define it.

/*
#include <stdint.h>
#include <stdlib.h>

typedef void (*fado_action)(void *context, uint64_t call, const char *action, const char *object);

static void fado_call_action(fado_action f, void *context, uint64_t call, const char *action, const char *object) {
	f(context, call, action, object);
}
*/
import "C"

import "unsafe"

// hostOf is the program's function as the library calls it, or nil when
// the program gave none.
func hostOf(f C.fado_action, context unsafe.Pointer) host {
	if f == nil {
		return nil
	}
	return func(call uint64, action, object string) {
		a, o := C.CString(action), C.CString(object)
		defer C.free(unsafe.Pointer(a))
		defer C.free(unsafe.Pointer(o))
		C.fado_call_action(f, context, C.uint64_t(call), a, o)
	}
}
