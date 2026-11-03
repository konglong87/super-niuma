package cli

import (
	"strings"
	"testing"
)

func TestParseImageGenerateArguments(t *testing.T) {
	req, err := parseImageCommand([]string{"generate", "--prompt", "a cat", "--size", "1536x1024", "--quality", "high", "--out", "out/cat.png"})
	if err != nil {
		t.Fatal(err)
	}
	if req.operation != "generate" || req.prompt != "a cat" || req.size != "1536x1024" || req.quality != "high" || req.out != "out/cat.png" {
		t.Fatalf("request = %+v", req)
	}
}

func TestParseImageEditRequiresSource(t *testing.T) {
	_, err := parseImageCommand([]string{"edit", "--prompt", "redraw"})
	if err == nil || !strings.Contains(err.Error(), "--image") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseImageRejectsUnknownOption(t *testing.T) {
	_, err := parseImageCommand([]string{"generate", "--prompt", "cat", "--unknown"})
	if err == nil || !strings.Contains(err.Error(), "unknown image option") {
		t.Fatalf("err = %v", err)
	}
}
