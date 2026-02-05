package contextcapture

import "testing"

func TestEncodeClaudeProjectPathCandidates(t *testing.T) {
	candidates := encodeClaudeProjectPathCandidates("/home/phileas/Sandbox/personal/covered_calls")
	if len(candidates) == 0 {
		t.Fatal("expected candidates")
	}
	if candidates[0] != "-home-phileas-Sandbox-personal-covered_calls" {
		t.Fatalf("unexpected candidate: %q", candidates[0])
	}
}
