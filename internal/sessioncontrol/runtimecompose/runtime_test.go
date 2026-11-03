package runtimecompose

import (
	"context"
	"errors"
	"testing"

	"github.com/konglong87/super-niuma/internal/sessioncontrol"
)

func TestNewServiceRejectsMissingProductionDependencies(t *testing.T) {
	if _, err := NewService(Dependencies{}); err == nil {
		t.Fatal("NewService() error = nil")
	}
}

func TestDisabledLocalSessionPortDoesNotExposeHostTranscripts(t *testing.T) {
	items, err := (disabledLocalSessionPort{}).List(context.Background(), sessioncontrol.ListRequest{Source: sessioncontrol.SourceLocal})
	if err != nil || len(items) != 0 {
		t.Fatalf("items=%v err=%v", items, err)
	}
	_, err = (disabledLocalSessionPort{}).Get(context.Background(), sessioncontrol.GetRequest{Ref: sessioncontrol.SessionRef{Source: sessioncontrol.SourceLocal, Key: "host-session"}})
	var serviceErr *sessioncontrol.ServiceError
	if !errors.As(err, &serviceErr) || serviceErr.Code != sessioncontrol.CodeForbidden {
		t.Fatalf("error=%v", err)
	}
}
