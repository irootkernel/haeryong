package main

/*
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#include <stdlib.h>
int hy_install(void*, const char*);
void hy_eval(void*, const char*);
char* hy_stats(void);
*/
import "C"

import (
	"encoding/json"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func installGuard(w application.Window, origin string) bool {
	v := C.CString(origin)
	defer C.free(unsafe.Pointer(v))
	return C.hy_install(w.NativeWindow(), v) == 1
}
func evalNative(w application.Window, script string) {
	v := C.CString(script)
	defer C.free(unsafe.Pointer(v))
	C.hy_eval(w.NativeWindow(), v)
}
func guardStats() map[string]any {
	p := C.hy_stats()
	defer C.free(unsafe.Pointer(p))
	var out map[string]any
	json.Unmarshal([]byte(C.GoString(p)), &out)
	return out
}
