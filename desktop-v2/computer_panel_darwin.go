//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit -framework Foundation
#include "computer_panel_darwin.h"
#include <stdlib.h>
*/
import "C"

import (
	"encoding/json"
	"sync"
	"unsafe"
)

type nativeComputerPanel struct{ owned *nativeComputerPanelState }

type nativeComputerPanelState struct {
	handle *C.goe2e_computer_panel

	mu      sync.Mutex
	closed  bool
	hasLast bool
	last    ComputerPanelSnapshot
}

func newNativeComputerPanel() computerPanel {
	handle := C.goe2e_computer_panel_create()
	if handle == nil {
		return nil
	}
	return &nativeComputerPanel{owned: &nativeComputerPanelState{handle: handle}}
}

func (p *nativeComputerPanel) state() *nativeComputerPanelState {
	if p == nil {
		return nil
	}
	return p.owned
}

func (p *nativeComputerPanel) Update(snapshot ComputerPanelSnapshot) {
	state := p.state()
	if state == nil {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed || (state.hasLast && state.last == snapshot) {
		return
	}
	state.last = snapshot
	state.hasLast = true
	handle := state.handle

	payload, err := json.Marshal(snapshot)
	if err != nil {
		return
	}
	cPayload := C.CString(string(payload))
	defer C.free(unsafe.Pointer(cPayload))
	C.goe2e_computer_panel_update(handle, cPayload)
}

func (p *nativeComputerPanel) Poll() *computerPanelCommand {
	state := p.state()
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return nil
	}
	handle := state.handle

	raw := C.goe2e_computer_panel_poll(handle)
	if raw == nil {
		return nil
	}
	defer C.goe2e_computer_panel_free_string(raw)

	var command computerPanelCommand
	if err := json.Unmarshal([]byte(C.GoString(raw)), &command); err != nil {
		return nil
	}
	switch command.Kind {
	case computerPanelCommandStop, computerPanelCommandPause, computerPanelCommandResume,
		computerPanelCommandExpand, computerPanelCommandCollapse, computerPanelCommandDismiss:
		return &command
	default:
		return nil
	}
}

func (p *nativeComputerPanel) Close() {
	state := p.state()
	if state == nil {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return
	}
	state.closed = true
	handle := state.handle

	C.goe2e_computer_panel_close(handle)
}
