//go:build !darwin

package main

type nativeComputerPanel struct{}

func newNativeComputerPanel() computerPanel {
	return nil
}

func (nativeComputerPanel) Update(ComputerPanelSnapshot) {}
func (nativeComputerPanel) Poll() *computerPanelCommand  { return nil }
func (nativeComputerPanel) Close()                       {}
