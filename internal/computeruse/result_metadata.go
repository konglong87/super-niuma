package computeruse

import "encoding/json"

// ResultMetadata preserves ComputerUse's bounded, structured execution evidence
// before generic output truncation. Window titles, input text and arbitrary
// result/error fields are deliberately excluded.
func ResultMetadata(output string) map[string]any {
	var root map[string]json.RawMessage
	if json.Unmarshal([]byte(output), &root) != nil {
		return nil
	}
	meta := map[string]any{}
	for _, key := range []string{"window_id", "bundle_id", "target_id", "session_id", "outcome", "error_code"} {
		var value string
		if json.Unmarshal(root[key], &value) == nil && value != "" {
			meta[key] = value
		}
	}
	var launch LaunchReceipt
	if json.Unmarshal(root["launch_receipt"], &launch) == nil {
		launch.Window.Title = ""
		meta["launch_receipt"] = launch
		if launch.Window.ID != "" {
			meta["window_id"] = launch.Window.ID
		}
		if launch.BundleID != "" {
			meta["bundle_id"] = launch.BundleID
		}
		if launch.TargetID != "" {
			meta["target_id"] = string(launch.TargetID)
		}
	}
	var obs Observation
	if json.Unmarshal(root["observation"], &obs) == nil {
		obs.ActiveWindow.Title = ""
		obs.Capabilities.TargetWindow.Title = ""
		meta["active_window"] = obs.ActiveWindow
		if obs.SessionID != "" {
			meta["session_id"] = obs.SessionID
		}
		if obs.WindowID != "" {
			meta["window_id"] = obs.WindowID
		}
		if obs.Capabilities.TargetWindow.ID != "" {
			meta["target_window"] = obs.Capabilities.TargetWindow
			meta["bundle_id"] = obs.Capabilities.TargetWindow.BundleID
		} else if obs.ActiveWindow.BundleID != "" {
			meta["bundle_id"] = obs.ActiveWindow.BundleID
		}
	}
	var receipt ActionReceipt
	if json.Unmarshal(root["receipt"], &receipt) == nil {
		receipt.ActiveWindowAfter.Title = ""
		receipt.ErrorMessage = ""
		receipt.RedactedActionSummary = ""
		receipt.EnvironmentFingerprint = ""
		receipt.Before, receipt.After = nil, nil
		if receipt.ErrorCode != "" {
			receipt.ErrorCode = PublicErrorCode(receipt.ErrorCode)
		}
		// Keep action/session/evidence IDs and acknowledged dispatch/completion
		// metadata even when generic text output is truncated.
		meta["receipt"] = receipt
		if receipt.DispatchState != "" {
			meta["dispatch_state"] = string(receipt.DispatchState)
		}
		if receipt.Outcome != "" {
			meta["outcome"] = string(receipt.Outcome)
		}
		if receipt.ErrorCode != "" {
			meta["error_code"] = PublicErrorCode(receipt.ErrorCode)
		}
		if receipt.ActiveWindowAfter.ID != "" {
			meta["active_window"] = receipt.ActiveWindowAfter
		}
	}
	if len(meta) == 0 {
		return nil
	}
	return meta
}
