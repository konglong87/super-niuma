//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit -framework Foundation
#include "computer_pointer_darwin.h"
#include <stdlib.h>
*/
import "C"

import (
	"encoding/json"
	"sync"
	"unsafe"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

type nativeComputerPointer struct{ state *nativeComputerPointerState }

type nativeComputerPointerState struct {
	mu     sync.Mutex
	handle *C.goe2e_computer_pointer
	closed bool
}

func newNativeComputerPointer() computerPointer {
	handle := C.goe2e_computer_pointer_create()
	if handle == nil {
		return nil
	}
	return &nativeComputerPointer{state: &nativeComputerPointerState{handle: handle}}
}

func (p *nativeComputerPointer) Show(cue cu.ActionVisualCue) {
	if p == nil || p.state == nil {
		return
	}
	payload, err := json.Marshal(struct {
		Visible bool               `json:"visible"`
		Cue     cu.ActionVisualCue `json:"cue"`
	}{Visible: true, Cue: cue})
	if err != nil {
		return
	}
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	if p.state.closed {
		return
	}
	value := C.CString(string(payload))
	defer C.free(unsafe.Pointer(value))
	C.goe2e_computer_pointer_show(p.state.handle, value)
}

func (p *nativeComputerPointer) Hide() {
	if p == nil || p.state == nil {
		return
	}
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	if !p.state.closed {
		C.goe2e_computer_pointer_hide(p.state.handle)
	}
}

func (p *nativeComputerPointer) Close() {
	if p == nil || p.state == nil {
		return
	}
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	if p.state.closed {
		return
	}
	p.state.closed = true
	C.goe2e_computer_pointer_close(p.state.handle)
}
