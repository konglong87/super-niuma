//go:build darwin && computeracceptance

package main

import (
	"encoding/json"
	"image"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestNativeComputerPointerScreenshot is an opt-in visual gate. It builds a
// tiny AppKit-hosted process so the real pointer window is created and driven
// on AppKit's main thread, then saves both a native desktop capture and the
// pointer view render. It never posts input or modifies the target application.
func TestNativeComputerPointerScreenshot(t *testing.T) {
	output := os.Getenv("COMPUTER_POINTER_ACCEPTANCE_OUTPUT")
	if output == "" {
		t.Skip("COMPUTER_POINTER_ACCEPTANCE_OUTPUT is not set")
	}
	if !filepath.IsAbs(output) {
		t.Fatal("pointer acceptance output must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		t.Fatal(err)
	}

	overlayOutput := output + ".overlay.png"
	geometryOutput := output + ".geometry.json"
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	helperSource := filepath.Join(repoRoot, "testdata", "computer_pointer_acceptance_main.m")
	nativeSource := filepath.Join(repoRoot, "computer_pointer_darwin.m")
	helperBinary := filepath.Join(t.TempDir(), "computer-pointer-acceptance")
	build := exec.Command("clang", "-x", "objective-c", nativeSource, helperSource,
		"-framework", "AppKit", "-framework", "Foundation", "-o", helperBinary)
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build native pointer acceptance helper: %v\n%s", err, out)
	}

	run := exec.Command(helperBinary, output, overlayOutput, geometryOutput)
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("run native pointer acceptance helper: %v\n%s", err, out)
	}

	image := decodePNG(t, output)
	if image.Bounds().Dx() == 0 || image.Bounds().Dy() == 0 {
		t.Fatal("native desktop screenshot has empty dimensions")
	}
	overlay := decodePNG(t, overlayOutput)
	// NSWindowSharingNone intentionally keeps the pointer out of authoritative
	// desktop captures. Inspect the actual AppKit view render separately.
	accentPixels := 0
	for y := overlay.Bounds().Min.Y; y < overlay.Bounds().Max.Y; y++ {
		for x := overlay.Bounds().Min.X; x < overlay.Bounds().Max.X; x++ {
			r, g, b, a := overlay.At(x, y).RGBA()
			if a > 30000 && b > r+10000 && b > g+5000 {
				accentPixels++
			}
		}
	}
	if accentPixels < 20 {
		t.Fatalf("native pointer overlay has no visible accent pixels: %d", accentPixels)
	}

	var geometry struct {
		GlobalX     float64 `json:"global_x"`
		GlobalY     float64 `json:"global_y"`
		AppKitX     float64 `json:"appkit_x"`
		AppKitY     float64 `json:"appkit_y"`
		PrimaryTop  float64 `json:"primary_top"`
		FrameX      float64 `json:"frame_x"`
		FrameY      float64 `json:"frame_y"`
		FrameWidth  float64 `json:"frame_width"`
		FrameHeight float64 `json:"frame_height"`
	}
	file, err := os.Open(geometryOutput)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewDecoder(file).Decode(&geometry); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	_ = file.Close()

	const globalX, globalY = 370.0, 560.0
	if math.Abs(geometry.GlobalX-globalX) > 0.5 || math.Abs(geometry.GlobalY-globalY) > 0.5 ||
		math.Abs(geometry.AppKitY-(geometry.PrimaryTop-globalY)) > 0.5 {
		t.Fatalf("pointer geometry = %+v, want global=(%v,%v) and AppKit Y=%v", geometry, globalX, globalY, geometry.PrimaryTop-globalY)
	}
	if geometry.FrameWidth <= 0 || geometry.FrameHeight <= 0 ||
		geometry.FrameX > geometry.AppKitX || geometry.FrameX+geometry.FrameWidth < geometry.AppKitX ||
		geometry.FrameY > geometry.AppKitY || geometry.FrameY+geometry.FrameHeight < geometry.AppKitY {
		t.Fatalf("pointer panel does not cover target AppKit point: %+v", geometry)
	}
}

func decodePNG(t *testing.T, path string) image.Image {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(file)
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}
