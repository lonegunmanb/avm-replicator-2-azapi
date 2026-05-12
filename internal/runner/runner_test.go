package runner

import (
	"testing"

	copilot "github.com/github/copilot-sdk/go"
)

// TestApproveAllUserIntent_ReturnsCLIAcceptedKind pins the user-intent layer
// of our "yolo" wiring. The embedded `@github/copilot` CLI only accepts a
// fixed vocabulary on the user-permission channel ("approve-once", "reject",
// "user-not-available", "no-result"). If a future SDK upgrade silently changes
// PermissionHandler.ApproveAll to return some other Kind, sessions will hang
// with `unexpected user permission response` — this test catches that early.
func TestApproveAllUserIntent_ReturnsCLIAcceptedKind(t *testing.T) {
	got, err := copilot.PermissionHandler.ApproveAll(copilot.PermissionRequest{}, copilot.PermissionInvocation{})
	if err != nil {
		t.Fatalf("ApproveAll returned error: %v", err)
	}

	accepted := map[copilot.PermissionRequestResultKind]bool{
		copilot.PermissionRequestResultKindApproved:         true, // "approve-once"
		copilot.PermissionRequestResultKindRejected:         true, // "reject"
		copilot.PermissionRequestResultKindUserNotAvailable: true, // "user-not-available"
	}
	if !accepted[got.Kind] {
		t.Fatalf("Kind=%q is not one of the CLI-accepted user-intent constants; "+
			"risks 'unexpected user permission response' or session hang", got.Kind)
	}

	// Defense in depth: pin the literal string too. If the SDK ever renames
	// the constant value (e.g. back to "approved"), the embedded CLI will
	// reject it and we want the failure to land here, not in production.
	switch string(got.Kind) {
	case "approve-once", "reject", "user-not-available":
		// ok
	default:
		t.Fatalf("Kind=%q has an unrecognised raw value; embedded Copilot CLI "+
			"only accepts approve-once / reject / user-not-available", got.Kind)
	}
}

// TestPreToolUseHookAllow_ContractShape pins the hooks-layer half of "yolo".
// The Copilot CLI gates each tool call through OnPreToolUse independently of
// the user-intent layer; returning PermissionDecision:"allow" is what keeps
// it from prompting/denying. This test guards the field name and value we
// rely on in NewSDKBackend.Run.
func TestPreToolUseHookAllow_ContractShape(t *testing.T) {
	hook := func(_ copilot.PreToolUseHookInput, _ copilot.HookInvocation) (*copilot.PreToolUseHookOutput, error) {
		return &copilot.PreToolUseHookOutput{PermissionDecision: "allow"}, nil
	}

	out, err := hook(copilot.PreToolUseHookInput{}, copilot.HookInvocation{})
	if err != nil {
		t.Fatalf("pre-tool-use hook returned error: %v", err)
	}
	if out == nil {
		t.Fatal("pre-tool-use hook returned nil output; CLI would treat as default-deny")
	}
	if out.PermissionDecision != "allow" {
		t.Fatalf("PermissionDecision=%q; must be \"allow\" to keep the hooks gate open", out.PermissionDecision)
	}
}
