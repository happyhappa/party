package contextcapture

import "testing"

func TestParseConfigYAML(t *testing.T) {
	cfg := DefaultConfig()
	data := []byte(`
# comment
session_log_path: /tmp/session.jsonl
recovery:
  tail_tokens: 123
  tail_bytes_per_token: 5
  tail_skip_summaries: 2
summary:
  chunk_tokens: 3000
  overlap_percent: 10
  rollup_every_n_chunks: 7
`)

	if err := parseConfigYAML(data, cfg); err != nil {
		t.Fatalf("parseConfigYAML: %v", err)
	}

	if cfg.SessionLogPath != "/tmp/session.jsonl" {
		t.Fatalf("session_log_path = %q", cfg.SessionLogPath)
	}
	if cfg.Recovery.TailTokens != 123 {
		t.Fatalf("tail_tokens = %d", cfg.Recovery.TailTokens)
	}
	if cfg.Recovery.TailBytesPerToken != 5 {
		t.Fatalf("tail_bytes_per_token = %d", cfg.Recovery.TailBytesPerToken)
	}
	if cfg.Recovery.TailSkipSummaries != 2 {
		t.Fatalf("tail_skip_summaries = %d", cfg.Recovery.TailSkipSummaries)
	}
	if cfg.Summary.ChunkTokens != 3000 {
		t.Fatalf("chunk_tokens = %d", cfg.Summary.ChunkTokens)
	}
	if cfg.Summary.OverlapPercent != 10 {
		t.Fatalf("overlap_percent = %d", cfg.Summary.OverlapPercent)
	}
	if cfg.Summary.RollupEveryNChunks != 7 {
		t.Fatalf("rollup_every_n_chunks = %d", cfg.Summary.RollupEveryNChunks)
	}
}
