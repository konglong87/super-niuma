//go:build !darwin

package main

import cu "github.com/konglong87/super-niuma/internal/computeruse"

type nativeComputerPointer struct{}

func newNativeComputerPointer() computerPointer       { return nil }
func (nativeComputerPointer) Show(cu.ActionVisualCue) {}
func (nativeComputerPointer) Hide()                   {}
func (nativeComputerPointer) Close()                  {}
