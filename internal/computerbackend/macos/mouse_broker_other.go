//go:build !darwin || !cgo

package macos

import cu "github.com/konglong87/super-niuma/internal/computeruse"

type unavailableMouseDriver struct{}

func newHostMouseDriver() mouseDriver                 { return unavailableMouseDriver{} }
func (unavailableMouseDriver) checkPermission() error { return errMouseBrokerUnavailable }
func (unavailableMouseDriver) buttonsHeld() bool      { return true }
func (unavailableMouseDriver) prepare(cu.MouseButton, float64, float64) (mouseGesture, error) {
	return nil, errMouseBrokerUnavailable
}
