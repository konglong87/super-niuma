package computerbridge

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

// Host is an authority-owned directory, not an approval API. Lookup must only
// expose an existing approved controller bound to the complete owner triple.
// Service methods must reauthorize each call; lookup is not a cached grant.
type Host interface {
	Service
	SessionLookup
}

// NewHandler handles authenticated local IPC. The caller must serve it only on
// an owner-only Unix socket, never on the public application's HTTP listener.
// Browser Origin and cross-host requests are rejected even with a valid token.
func NewHandler(token string, host Host) (http.Handler, error) {
	if !validToken(token) || host == nil {
		return nil, ErrInvalidConfig
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Header.Get("Origin") != "" || r.Host != "localhost" ||
			subtle.ConstantTimeCompare([]byte(r.Header.Get(AuthorizationHeader)), []byte(BearerPrefix+token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != CommandPath || r.URL.RawQuery != "" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		defer r.Body.Close()
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxRequestBytes))
		if err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		var req Request
		if decodeStrict(body, &req) != nil || !validRequest(req) {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		data, err := dispatchHost(r, host, req)
		out := Response{}
		if err != nil {
			out.Error = "host operation failed"
			var coded interface{ Code() string }
			if errors.As(err, &coded) {
				out.ErrorCode = cu.PublicErrorCode(coded.Code())
			}
		}
		// Only a bound Execute receipt survives errors. Never return arbitrary
		// backend strings, images from a failing reader, or partial capabilities.
		if err == nil || req.Op == OpExecute || req.Op == OpExecuteTurn || req.Op == OpLaunchApp {
			if data != nil {
				out.Data, _ = json.Marshal(data)
			}
		}
		encoded, encodeErr := json.Marshal(out)
		if encodeErr != nil || len(encoded) > MaxResponseBytes {
			http.Error(w, "response unavailable", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(encoded)
	}), nil
}

func dispatchHost(r *http.Request, host Host, q Request) (any, error) {
	ctx := r.Context()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch q.Op {
	case OpLookup:
		id, err := host.Lookup(ctx, q.Owner)
		if err == nil && !validID(id) {
			err = ErrInvalidResponse
		}
		return LookupResponse{SessionID: id}, err
	case OpEnsure:
		if q.StartupAttemptID != "" {
			coordinator, ok := host.(SessionAttemptCoordinator)
			if !ok {
				return nil, ErrRemote
			}
			id, err := coordinator.EnsureComputerSessionAttempt(ctx, q.Owner, q.StartupAttemptID)
			if err == nil && !validID(id) {
				err = ErrInvalidResponse
			}
			return SessionResponse{SessionID: id, StartupAttemptID: q.StartupAttemptID}, err
		}
		coordinator, ok := host.(SessionCoordinator)
		if !ok {
			return nil, ErrRemote
		}
		id, err := coordinator.EnsureComputerSession(ctx, q.Owner)
		if err == nil && !validID(id) {
			err = ErrInvalidResponse
		}
		return SessionResponse{SessionID: id}, err
	case OpResolveEnsure:
		resolver, ok := host.(SessionAttemptResolver)
		if !ok {
			return nil, ErrRemote
		}
		id, err := resolver.ResolveComputerSessionStart(ctx, q.Owner, q.StartupAttemptID)
		if err == nil && !validID(id) {
			err = ErrInvalidResponse
		}
		return SessionResponse{SessionID: id, StartupAttemptID: q.StartupAttemptID}, err
	case OpRuntimeStatus:
		status, ok := host.(RuntimeStatusService)
		if !ok {
			return nil, ErrRemote
		}
		return status.RuntimeStatus(ctx, q.Owner)
	case OpRuntimeStop:
		stopper, ok := host.(RuntimeStopService)
		if !ok {
			return nil, ErrRemote
		}
		return SessionResponse{}, stopper.StopRuntime(ctx, q.Owner)
	case OpVisualCue:
		reader, ok := host.(VisualCueReader)
		if !ok {
			return nil, ErrRemote
		}
		response, err := reader.VisualCue(ctx, q.Owner, q.SessionID)
		if err != nil {
			return nil, err
		}
		if !validVisualCueResponse(response, q.SessionID) {
			return nil, ErrInvalidResponse
		}
		return response, nil
	case OpStartLocal:
		starter, ok := host.(LocalSessionStarter)
		if !ok {
			return nil, ErrRemote
		}
		return starter.StartLocal(ctx, q.Owner, q.Approved)
	case OpSnapshot:
		reader, ok := host.(SessionSnapshotReader)
		if !ok {
			return nil, ErrRemote
		}
		return reader.Snapshot(ctx, q.Owner, q.SessionID)
	case OpCapabilities:
		return host.Capabilities(ctx, q.Owner, q.SessionID)
	case OpLaunchApp:
		if q.TargetID != "" {
			launcher, ok := host.(cu.TargetLauncher)
			if !ok {
				return nil, ErrRemote
			}
			receipt, err := launcher.LaunchTarget(ctx, q.Owner, q.SessionID, cu.TargetID(q.TargetID))
			if !validLaunchReceipt(receipt, q.SessionID, q.TargetID, "") {
				return nil, ErrInvalidResponse
			}
			if receipt.ErrorCode != "" {
				receipt.ErrorCode = cu.PublicErrorCode(receipt.ErrorCode)
			}
			return receipt, err
		}
		// Legacy application launch remains available only for older adapters.
		launcher, ok := host.(cu.ApplicationLauncher)
		if !ok {
			return nil, ErrRemote
		}
		receipt, err := launcher.LaunchApp(ctx, q.Owner, q.SessionID, q.Application)
		if !validLaunchReceipt(receipt, q.SessionID, "", q.Application) {
			return nil, ErrInvalidResponse
		}
		if receipt.ErrorCode != "" {
			receipt.ErrorCode = cu.PublicErrorCode(receipt.ErrorCode)
		}
		return receipt, err
	case OpObserve:
		return host.Observe(ctx, q.Owner, *q.ObserveRequest)
	case OpExecute:
		receipt, err := host.Execute(ctx, q.Owner, *q.Action)
		if !validReceipt(receipt, *q.Action) {
			// Dispatch may already have happened. Never reclassify it as rejected.
			return cu.ActionReceipt{ActionID: q.Action.ID, SessionID: q.SessionID, BeforeObservationID: q.Action.ObservationID,
				Outcome: cu.OutcomeUnknown, Verification: cu.VerificationUnknown, RedactedActionSummary: string(q.Action.Kind)}, ErrInvalidResponse
		}
		receipt.ErrorMessage = ""
		receipt.RedactedActionSummary = string(q.Action.Kind)
		if receipt.ErrorCode != "" {
			receipt.ErrorCode = cu.PublicErrorCode(receipt.ErrorCode)
		}
		return receipt, err
	case OpExecuteTurn:
		turnService, ok := host.(cu.TurnService)
		if !ok {
			return nil, ErrRemote
		}
		result, err := turnService.ExecuteTurn(ctx, q.Owner, *q.Action)
		if !validTurnResultForAction(result, *q.Action) {
			// A malformed response after dispatch is ambiguous. Preserve a bound
			// unknown result instead of manufacturing a rejection or zero receipt.
			return unknownTurnResult(*q.Action), ErrInvalidResponse
		}
		out := ExecuteTurnResponse{Result: result}
		if result.ScreenshotState == cu.ScreenshotReady {
			data, media := result.ScreenshotData, "image/png"
			if len(data) == 0 {
				reader, ok := host.(TurnScreenshotReader)
				if !ok {
					return out, ErrRemote
				}
				var readErr error
				data, media, readErr = reader.TurnScreenshot(ctx, q.Owner, result)
				if readErr != nil {
					return out, readErr
				}
			}
			out.ScreenshotData = base64.StdEncoding.EncodeToString(data)
			out.ScreenshotMediaType = media
			if result.Observation != nil {
				out.ScreenshotExpiresAt = result.Observation.ExpiresAt
			}
			if err := validateTurnScreenshot(out); err != nil {
				return out, err
			}
		}
		return out, err
	case OpPause:
		return SessionResponse{SessionID: q.SessionID}, host.Pause(ctx, q.Owner, q.SessionID)
	case OpResume:
		return SessionResponse{SessionID: q.SessionID}, host.Resume(ctx, q.Owner, q.SessionID)
	case OpStop:
		return SessionResponse{SessionID: q.SessionID}, host.Stop(ctx, q.Owner, q.SessionID)
	case OpImage:
		data, media, err := host.ObservationImage(ctx, q.Owner, q.SessionID, q.ObservationID)
		if err != nil {
			return nil, err
		}
		if len(data) == 0 || len(data) > MaxImageBytes || media != "image/png" {
			return nil, ErrInvalidImage
		}
		return ImageResponse{SessionID: q.SessionID, ObservationID: q.ObservationID, MediaType: media, ImageData: base64.StdEncoding.EncodeToString(data)}, nil
	default:
		return nil, ErrInvalidRequest
	}
}
