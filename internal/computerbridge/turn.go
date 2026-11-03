package computerbridge

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"time"

	"github.com/google/uuid"
	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

var _ cu.TurnService = (*Client)(nil)

// ExecuteTurn is the provider-facing atomic RPC. It never falls back to the
// legacy execute/image pair. A response that is valid enough to bind to the
// action is returned together with its error so an executed/unknown receipt is
// never replaced by a zero value.
func (c *Client) ExecuteTurn(ctx context.Context, owner cu.SessionOwner, action cu.Action) (cu.ComputerTurnResult, error) {
	response, attempted, callErr := c.call(ctx, Request{Op: OpExecuteTurn, Owner: owner, SessionID: action.SessionID, Action: &action})
	if !attempted {
		return cu.ComputerTurnResult{}, callErr
	}
	var out ExecuteTurnResponse
	if decodeStrict(response.Data, &out) != nil || !validTurnResultForAction(out.Result, action) {
		return unknownTurnResult(action), joinTurnError(callErr, ErrInvalidResponse)
	}
	if out.Result.ScreenshotState == cu.ScreenshotReady {
		data, err := validatedTurnScreenshotData(out)
		if err != nil {
			return out.Result, joinTurnError(callErr, err)
		}
		out.Result.ScreenshotData = data
	} else if out.ScreenshotData != "" || out.ScreenshotMediaType != "" || !out.ScreenshotExpiresAt.IsZero() {
		return out.Result, joinTurnError(callErr, ErrInvalidResponse)
	}
	if callErr != nil {
		return out.Result, callErr
	}
	return out.Result, nil
}

func validateTurnScreenshot(response ExecuteTurnResponse) error {
	_, err := validatedTurnScreenshotData(response)
	return err
}

func validatedTurnScreenshotData(response ExecuteTurnResponse) ([]byte, error) {
	result := response.Result
	if result.Screenshot == nil || result.Observation == nil || result.ObservationState != cu.ObservationReady || result.Observation.ExpiresAt.IsZero() {
		return nil, ErrInvalidImage
	}
	if response.ScreenshotMediaType != pngMediaType || response.ScreenshotData == "" || response.ScreenshotExpiresAt.IsZero() || !response.ScreenshotExpiresAt.Equal(result.Observation.ExpiresAt) || !response.ScreenshotExpiresAt.After(time.Now()) {
		return nil, ErrInvalidImage
	}
	data, media, err := decodeImage(ImageResponse{MediaType: response.ScreenshotMediaType, ImageData: response.ScreenshotData})
	if err != nil {
		return nil, err
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width != result.Screenshot.Width || config.Height != result.Screenshot.Height {
		return nil, ErrInvalidImage
	}
	ref := cu.NewMediaRef(result.Screenshot.ID, media, data, config.Width, config.Height)
	if ref.MediaType != result.Screenshot.MediaType || ref.SHA256 != result.Screenshot.SHA256 || ref.SizeBytes != result.Screenshot.SizeBytes || ref.Width != result.Screenshot.Width || ref.Height != result.Screenshot.Height {
		return nil, ErrInvalidImage
	}
	return data, nil
}

func unknownTurnResult(action cu.Action) cu.ComputerTurnResult {
	now := time.Now()
	receipt := cu.ActionReceipt{
		ActionID:              action.ID,
		SessionID:             action.SessionID,
		BeforeObservationID:   action.ObservationID,
		Outcome:               cu.OutcomeUnknown,
		DispatchState:         cu.DispatchUnknown,
		Verification:          cu.VerificationUnknown,
		RedactedActionSummary: action.RedactedSummary(),
		ErrorCode:             cu.ErrorCodeInputUncertain,
		CompletedAt:           now,
	}
	result := cu.ComputerTurnResult{
		ProtocolVersion:  cu.ProtocolVersion,
		TurnID:           uuid.NewString(),
		SessionID:        action.SessionID,
		ActionID:         action.ID,
		ActionKind:       action.Kind,
		DispatchState:    cu.DispatchUnknown,
		Outcome:          cu.OutcomeUnknown,
		Verification:     cu.VerificationUnknown,
		Receipt:          receipt,
		ObservationState: cu.ObservationInvalidated,
		ScreenshotState:  cu.ScreenshotNotRequested,
		ErrorCode:        cu.ErrorCodeInputUncertain,
		RetryPolicy:      cu.RetryNever,
		Sequence:         1,
		StartedAt:        now,
		CompletedAt:      now,
	}
	if action.Kind == cu.ActionLaunchApp {
		result.LaunchReceipt = &cu.LaunchReceipt{
			TargetID: action.TargetID, Application: cu.ComputerApplication(action.TargetID),
			Outcome: cu.OutcomeUnknown, ErrorCode: cu.ErrorCodeInputUncertain, CompletedAt: now,
		}
	}
	return result
}

func joinTurnError(first, second error) error {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return errors.Join(first, second)
}
