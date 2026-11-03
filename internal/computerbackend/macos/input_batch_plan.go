package macos

import (
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

// A batch is a single bounded balanced gesture, never an entire typing action.
// Coordinates and key data are derived by the host from the authorized action;
// the private helper channel can only advance this immutable plan.
type inputBatchOperation struct {
	kind       cu.ActionKind
	button     cu.MouseButton
	x, y       float64
	clickCount int
	keyCode    uint16
	flags      uint64
	scalar     rune
}
type inputBatchDriver interface {
	prepareBatch(inputBatchOperation) (preparedInputBatch, error)
}

// prepareBatch allocates every down/up (including modifiers) before returning.
// Once started commit must finish the bounded sequence despite helper death or
// cancellation. CGEventPost has no delivery ACK: completion is not visual proof.
type preparedInputBatch interface {
	commit()
	close()
}

const (
	batchFlagShift   uint64 = 1 << 17
	batchFlagControl uint64 = 1 << 18
	batchFlagOption  uint64 = 1 << 19
	batchFlagCommand uint64 = 1 << 20
)

var batchKeyCodes = map[string]uint16{
	"return": 36, "enter": 36, "tab": 48, "space": 49, "delete": 51, "backspace": 51, "escape": 53, "esc": 53,
	"left": 123, "right": 124, "down": 125, "up": 126, "a": 0, "b": 11, "c": 8, "d": 2, "e": 14, "f": 3, "g": 5, "h": 4, "i": 34, "j": 38, "k": 40, "l": 37, "m": 46,
	"n": 45, "o": 31, "p": 35, "q": 12, "r": 15, "s": 1, "t": 17, "u": 32, "v": 9, "w": 13, "x": 7, "y": 16, "z": 6,
	"0": 29, "1": 18, "2": 19, "3": 20, "4": 21, "5": 23, "6": 22, "7": 26, "8": 28, "9": 25,
}
var batchModifierFlags = map[string]uint64{
	"command": batchFlagCommand, "cmd": batchFlagCommand, "meta": batchFlagCommand,
	"control": batchFlagControl, "ctrl": batchFlagControl, "shift": batchFlagShift, "option": batchFlagOption, "alt": batchFlagOption,
}

func isBatchAction(kind cu.ActionKind) bool {
	switch kind {
	case cu.ActionClick, cu.ActionDoubleClick, cu.ActionRightClick, cu.ActionKey, cu.ActionHotkey, cu.ActionType:
		return true
	}
	return false
}
func planInputBatches(action cu.Action, obs cu.Observation) ([]inputBatchOperation, error) {
	invalid := errors.New("invalid host input batch plan")
	op := inputBatchOperation{kind: action.Kind}
	switch action.Kind {
	case cu.ActionClick, cu.ActionDoubleClick, cu.ActionRightClick:
		bounds := obs.Capabilities.CoordinateSpace.Bounds
		if bounds == nil || action.Point == nil || obs.Width <= 0 || obs.Height <= 0 || bounds.Width <= 0 || bounds.Height <= 0 {
			return nil, invalid
		}
		p := action.Point
		if p.X < 0 || p.Y < 0 || p.X >= obs.Width || p.Y >= obs.Height {
			return nil, invalid
		}
		op.x = bounds.X + float64(p.X)*bounds.Width/float64(obs.Width)
		op.y = bounds.Y + float64(p.Y)*bounds.Height/float64(obs.Height)
		if math.IsNaN(op.x) || math.IsInf(op.x, 0) || math.IsNaN(op.y) || math.IsInf(op.y, 0) {
			return nil, invalid
		}
		op.button, op.clickCount = cu.MouseButtonLeft, 1
		if action.Kind == cu.ActionRightClick {
			op.button = cu.MouseButtonRight
		}
		ops := []inputBatchOperation{op}
		if action.Kind == cu.ActionDoubleClick {
			op.clickCount = 2
			ops = append(ops, op)
		}
		return ops, nil
	case cu.ActionType:
		if action.Text == "" || len(action.Text) > cu.MaxInputBytes || !utf8.ValidString(action.Text) {
			return nil, invalid
		}
		ops := make([]inputBatchOperation, 0, utf8.RuneCountInString(action.Text))
		for _, r := range action.Text {
			ops = append(ops, inputBatchOperation{kind: action.Kind, scalar: r})
		}
		return ops, nil
	case cu.ActionKey:
		code, ok := batchKeyCodes[strings.ToLower(action.Key)]
		if !ok {
			return nil, invalid
		}
		op.keyCode = code
	case cu.ActionHotkey:
		if len(action.Keys) < 2 || len(action.Keys) > cu.MaxHotkeyKeys {
			return nil, invalid
		}
		found := false
		for _, name := range action.Keys {
			name = strings.ToLower(name)
			if flag, ok := batchModifierFlags[name]; ok {
				if op.flags&flag != 0 {
					return nil, invalid
				}
				op.flags |= flag
			} else {
				code, ok := batchKeyCodes[name]
				if !ok || found {
					return nil, invalid
				}
				op.keyCode = code
				found = true
			}
		}
		if !found || op.flags == 0 {
			return nil, invalid
		}
	default:
		return nil, invalid
	}
	return []inputBatchOperation{op}, nil
}
