package harness

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The approval deadline is protocol surface: every adapter shares it and
// the Host reads its cause, so its two guarantees are pinned here rather
// than only through whichever adapter happens to be under test.

// TestApprovalContextCarriesTheTimeoutCause proves expiry is
// distinguishable from a cancelled run. The Host settles the card from
// this cause, and "approval timed out" versus "cancelled" is the whole
// point of using WithTimeoutCause rather than a bare WithTimeout.
func TestApprovalContextCarriesTheTimeoutCause(t *testing.T) {
	ctx, cancel := ApprovalContext(context.Background(), 20*time.Millisecond)
	defer cancel()

	<-ctx.Done()
	if !errors.Is(context.Cause(ctx), ErrApprovalTimedOut) {
		t.Fatalf("expiry cause = %v, want ErrApprovalTimedOut", context.Cause(ctx))
	}
	if got := ApprovalReason(ctx); got != "approval timed out" {
		t.Fatalf("ApprovalReason = %q, want %q", got, "approval timed out")
	}
}

// TestApprovalReasonDistinguishesCancellation proves a parent cancel is
// reported as a cancellation, not as a timeout: the two mean different
// things to the user looking at a card.
func TestApprovalReasonDistinguishesCancellation(t *testing.T) {
	base, cancelBase := context.WithCancel(context.Background())
	ctx, cancel := ApprovalContext(base, time.Hour) // long: the parent must win
	defer cancel()

	cancelBase()
	<-ctx.Done()
	if errors.Is(context.Cause(ctx), ErrApprovalTimedOut) {
		t.Fatal("a parent cancellation must not report as a timeout")
	}
	if got := ApprovalReason(ctx); got != "cancelled" {
		t.Fatalf("ApprovalReason = %q, want %q", got, "cancelled")
	}
}

// TestApprovalContextFallsBackToTheDefault proves a zero or negative
// timeout still bounds the wait, which is the failure mode that leaves a
// card pending forever.
func TestApprovalContextFallsBackToTheDefault(t *testing.T) {
	for _, timeout := range []time.Duration{0, -time.Second} {
		ctx, cancel := ApprovalContext(context.Background(), timeout)
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatalf("timeout %v produced no deadline", timeout)
		}
		if left := time.Until(deadline); left <= 0 || left > DefaultApprovalTimeout {
			t.Fatalf("timeout %v gave a deadline %v away, want within the %v default",
				timeout, left, DefaultApprovalTimeout)
		}
		cancel()
	}
}

// TestApprovalContextInheritsAnEarlierParentDeadline proves the helper
// never extends a deadline the parent already set: a turn's own context
// must still bound the wait.
func TestApprovalContextInheritsAnEarlierParentDeadline(t *testing.T) {
	parent, cancelParent := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelParent()
	ctx, cancel := ApprovalContext(parent, time.Hour)
	defer cancel()

	<-ctx.Done()
	if time.Since(parentDeadline(t, ctx)) > time.Second {
		t.Fatal("the helper replaced the parent's shorter deadline")
	}
}

func parentDeadline(t *testing.T, ctx context.Context) time.Time {
	t.Helper()
	d, ok := ctx.Deadline()
	if !ok {
		t.Fatal("no deadline")
	}
	return d
}

// TestPolicyFailsClosed pins the resolution order the spec promises:
// selector rule, then mode default, and anything unknown denies. It lives
// here because this is the package the rules are defined in, even though
// the loop exercises them.
func TestPolicyFailsClosed(t *testing.T) {
	cases := []struct {
		name    string
		pol     Policy
		tool    string
		actions []Action
		want    Permission
	}{
		{"read-only denies shell", Policy{Mode: ModeReadOnly}, "bash", []Action{ActionShell}, PermDeny},
		{"read-only denies write", Policy{Mode: ModeReadOnly}, "edit_file", []Action{ActionFileWrite}, PermDeny},
		{"read-only allows read", Policy{Mode: ModeReadOnly}, "read_file", []Action{ActionFileRead}, PermAllow},
		{"agent allows shell", Policy{Mode: ModeAgent}, "bash", []Action{ActionShell}, PermAllow},
		{"agent asks for mcp", Policy{Mode: ModeAgent}, "mcp_x", []Action{ActionMCP}, PermAsk},
		{"full allows mcp", Policy{Mode: ModeFull}, "mcp_x", []Action{ActionMCP}, PermAllow},
		{"no declared action denies", Policy{Mode: ModeFull}, "mystery", nil, PermDeny},
		{"unknown action denies", Policy{Mode: ModeFull}, "mystery", []Action{"nope"}, PermDeny},
		{"rule overrides mode", Policy{Mode: ModeFull, Rules: Rules{"bash": PermAsk}}, "bash", []Action{ActionShell}, PermAsk},
		{"rule can deny in full", Policy{Mode: ModeFull, Rules: Rules{"edit_file": PermDeny}}, "edit_file", []Action{ActionFileWrite}, PermDeny},
		{"longest prefix wins", Policy{Mode: ModeFull, Rules: Rules{"mcp_*": PermAsk, "mcp_safe_*": PermAllow}}, "mcp_safe_thing", []Action{ActionMCP}, PermAllow},
		{"bare star applies", Policy{Mode: ModeReadOnly, Rules: Rules{"*": PermAllow}}, "bash", []Action{ActionShell}, PermAllow},
		{"malformed rule denies", Policy{Mode: ModeFull, Rules: Rules{"bash": "alow"}}, "bash", []Action{ActionShell}, PermDeny},
		{"a deny is not shadowed by a shorter allow", Policy{Mode: ModeReadOnly, Rules: Rules{"bash": PermDeny, "bash_safe": PermAllow}}, "bash", []Action{ActionShell}, PermDeny},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.pol.Resolve(tc.tool, tc.actions); got != tc.want {
				t.Fatalf("Resolve(%q) = %q, want %q", tc.tool, got, tc.want)
			}
		})
	}
}

// TestPermissionFromConfigFailsClosed proves a typo never reads as
// permission to run.
func TestPermissionFromConfigFailsClosed(t *testing.T) {
	for in, want := range map[string]Permission{
		"allow": PermAllow, "ALLOW": PermAllow, " allow ": PermAllow,
		"deny": PermDeny, "Deny": PermDeny,
		"ask": PermAsk, " ASK\n": PermAsk,
		"alow": PermDeny, "": PermDeny, "allowed": PermDeny, "maybe": PermDeny,
	} {
		if got := PermissionFromConfig(in); got != want {
			t.Errorf("PermissionFromConfig(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestApprovalSummaryRedacts proves the summary the user decides on never
// carries a credential (approvals.md, "Redaction").
func TestApprovalSummaryRedacts(t *testing.T) {
	var call ToolCall
	call.Function.Name = "bash"
	call.Function.Arguments = `{"command":"curl -H 'Authorization: Bearer sk-abcdefgh12345678' https://api.test"}`
	got := ApprovalSummary(call)
	if contains(got, "sk-abcdefgh12345678") {
		t.Fatalf("ApprovalSummary leaks the token: %q", got)
	}
	if !contains(got, "Authorization") {
		t.Fatalf("ApprovalSummary should keep the key visible: %q", got)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
