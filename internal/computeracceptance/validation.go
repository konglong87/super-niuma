package computeracceptance

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"unicode/utf8"
)

func decodeReport(body []byte) (Report, error) {
	var report Report
	if !utf8.Valid(body) {
		return report, errors.New("UTF-8 required")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return report, errors.New("expected report object with known fields")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return report, errors.New("expected exactly one JSON object")
	}
	return report, validateReport(report)
}

func validateReport(report Report) error {
	if len(report.Events) > MaxBatchEvents {
		return errors.New("too many events")
	}
	if report.Geometry == nil || report.State == nil {
		return errors.New("geometry and state required")
	}
	if !validGeometry(*report.Geometry) || !validState(*report.State) {
		return errors.New("invalid geometry or state")
	}
	for _, event := range report.Events {
		if !validEvent(event) {
			return errors.New("invalid event type, target, text, or numeric bounds")
		}
	}
	return nil
}

func validEvent(event DOMEvent) bool {
	if !validEventType(event.Type) || !validTarget(event.Target) {
		return false
	}
	for _, text := range []string{event.Key, event.Code, event.InputType} {
		if len(text) > maxLabelBytes {
			return false
		}
	}
	if len(event.Data) > MaxTextBytes || len(event.Value) > MaxTextBytes {
		return false
	}
	return finiteBetween(event.TimeStamp, 0, 1e15) &&
		validPoint(event.Client) && validPoint(event.Screen) &&
		nonnegative(event.ScrollTop, event.ScrollLeft) &&
		bounded(event.DeltaX, event.DeltaY) &&
		event.Button >= -1 && event.Button <= 5 &&
		event.Buttons >= 0 && event.Buttons <= 31 &&
		event.Detail >= 0 && event.Detail <= 1000 &&
		event.DeltaMode >= 0 && event.DeltaMode <= 2
}

func validEventType(kind EventType) bool {
	switch kind {
	case Click, DoubleClick, ContextMenu, MouseMove, MouseDown, MouseUp,
		Wheel, Scroll, KeyDown, KeyUp, BeforeInput, Input,
		CompositionStart, CompositionUpdate, CompositionEnd, Focus, Blur:
		return true
	}
	return false
}

func validTarget(id TargetID) bool {
	switch id {
	case ClickTarget, DoubleClickTarget, ContextMenuTarget, MouseMoveTarget, ScrollTarget, TextTarget, DragSourceTarget, DragDropTarget:
		return true
	}
	return false
}

func validState(state PageState) bool {
	return len(state.InputValue) <= MaxTextBytes &&
		state.SelectionStart >= 0 && state.SelectionEnd >= state.SelectionStart &&
		state.SelectionEnd <= MaxTextBytes && nonnegative(state.ScrollTop, state.ScrollLeft)
}

func validGeometry(geometry Geometry) bool {
	if !validPoint(geometry.WindowScreen) || !validPoint(geometry.PageScroll) ||
		!nonnegative(geometry.OuterWidth, geometry.OuterHeight, geometry.ScreenWidth, geometry.ScreenHeight) ||
		!positive(geometry.InnerWidth, geometry.InnerHeight, geometry.DevicePixelRatio) ||
		!validRect(geometry.AvailableScreen) || len(geometry.Targets) == 0 || len(geometry.Targets) > targetCount {
		return false
	}
	if viewport := geometry.VisualViewport; viewport != nil {
		if !bounded(viewport.OffsetLeft, viewport.OffsetTop, viewport.PageLeft, viewport.PageTop) ||
			!positive(viewport.Width, viewport.Height, viewport.Scale) {
			return false
		}
	}
	if calibration := geometry.Calibration; calibration != nil {
		if !validPoint(calibration.Client) || !validPoint(calibration.Screen) ||
			!finiteBetween(calibration.TimeStamp, 0, 1e15) {
			return false
		}
	}
	return validTargets(geometry.Targets)
}

func validTargets(targets []TargetGeometry) bool {
	seen := make(map[TargetID]bool, len(targets))
	for _, target := range targets {
		if !validTarget(target.ID) || seen[target.ID] || !validRect(target.Rect) || !validPoint(target.Center) {
			return false
		}
		seen[target.ID] = true
	}
	return true
}

func validRect(rect Rect) bool {
	return bounded(rect.X, rect.Y) && nonnegative(rect.Width, rect.Height)
}

func validPoint(point Point) bool { return bounded(point.X, point.Y) }

func bounded(values ...float64) bool {
	for _, value := range values {
		if !finiteBetween(value, -maxCoordinate, maxCoordinate) {
			return false
		}
	}
	return true
}

func nonnegative(values ...float64) bool {
	for _, value := range values {
		if !finiteBetween(value, 0, maxCoordinate) {
			return false
		}
	}
	return true
}

func positive(values ...float64) bool {
	for _, value := range values {
		if !finiteBetween(value, math.SmallestNonzeroFloat64, maxCoordinate) {
			return false
		}
	}
	return true
}

func finiteBetween(value, min, max float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= min && value <= max
}
