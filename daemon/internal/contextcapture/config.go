package contextcapture

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultTailTokens        = 2000
	defaultTailBytesPerToken = 4
	defaultTailSkipSummaries = 1

	defaultChunkTokens       = 4000
	defaultOverlapPercent    = 12
	defaultRollupEveryChunks = 5
)

// Config holds context capture configuration loaded from YAML.
type Config struct {
	SessionLogPath string
	Recovery       RecoveryConfig
	Summary        SummaryConfig
}

// RecoveryConfig controls tail extraction behavior.
type RecoveryConfig struct {
	TailTokens        int
	TailBytesPerToken int
	TailSkipSummaries int
}

// SummaryConfig controls summary chunking behavior.
type SummaryConfig struct {
	ChunkTokens        int
	OverlapPercent     int
	RollupEveryNChunks int
}

// DefaultConfig returns default configuration values.
func DefaultConfig() *Config {
	return &Config{
		SessionLogPath: "",
		Recovery: RecoveryConfig{
			TailTokens:        defaultTailTokens,
			TailBytesPerToken: defaultTailBytesPerToken,
			TailSkipSummaries: defaultTailSkipSummaries,
		},
		Summary: SummaryConfig{
			ChunkTokens:        defaultChunkTokens,
			OverlapPercent:     defaultOverlapPercent,
			RollupEveryNChunks: defaultRollupEveryChunks,
		},
	}
}

// Load loads configuration from RELAY_CONTEXT_CAPTURE_CONFIG if set,
// otherwise ~/llm-share/config/context-capture.yaml. Creates a sample file
// if none exists.
func Load() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return DefaultConfig(), err
	}

	return LoadFromPath(path)
}

// LoadFromPath loads configuration from a provided path, creating a sample if missing.
func LoadFromPath(path string) (*Config, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if writeErr := writeSampleConfig(path, cfg); writeErr != nil {
				return cfg, writeErr
			}
			return cfg, nil
		}
		return cfg, err
	}

	if err := parseConfigYAML(data, cfg); err != nil {
		return cfg, err
	}

	applyDefaults(cfg)
	return cfg, nil
}

func configPath() (string, error) {
	if configured := os.Getenv("RELAY_CONTEXT_CAPTURE_CONFIG"); configured != "" {
		return configured, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "llm-share", "config", "context-capture.yaml"), nil
}

func writeSampleConfig(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	sample := fmt.Sprintf(
		"session_log_path:\nrecovery:\n  tail_tokens: %d\n  tail_bytes_per_token: %d\n  tail_skip_summaries: %d\nsummary:\n  chunk_tokens: %d\n  overlap_percent: %d\n  rollup_every_n_chunks: %d\n",
		cfg.Recovery.TailTokens,
		cfg.Recovery.TailBytesPerToken,
		cfg.Recovery.TailSkipSummaries,
		cfg.Summary.ChunkTokens,
		cfg.Summary.OverlapPercent,
		cfg.Summary.RollupEveryNChunks,
	)

	return os.WriteFile(path, []byte(sample), 0o644)
}

func applyDefaults(cfg *Config) {
	if cfg.Recovery.TailTokens == 0 {
		cfg.Recovery.TailTokens = defaultTailTokens
	}
	if cfg.Recovery.TailBytesPerToken == 0 {
		cfg.Recovery.TailBytesPerToken = defaultTailBytesPerToken
	}
	if cfg.Recovery.TailSkipSummaries == 0 {
		cfg.Recovery.TailSkipSummaries = defaultTailSkipSummaries
	}
	if cfg.Summary.ChunkTokens == 0 {
		cfg.Summary.ChunkTokens = defaultChunkTokens
	}
	if cfg.Summary.OverlapPercent == 0 {
		cfg.Summary.OverlapPercent = defaultOverlapPercent
	}
	if cfg.Summary.RollupEveryNChunks == 0 {
		cfg.Summary.RollupEveryNChunks = defaultRollupEveryChunks
	}
}

func parseConfigYAML(data []byte, cfg *Config) error {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	section := ""
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.HasSuffix(line, ":") {
			section = strings.TrimSuffix(line, ":")
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid config line %d: %q", lineNum, line)
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		value = strings.Trim(value, "\"'")

		switch section {
		case "":
			if key == "session_log_path" {
				cfg.SessionLogPath = value
				continue
			}
		}

		parsed, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid config value on line %d: %q", lineNum, line)
		}

		switch section {
		case "":
			continue
		case "recovery":
			switch key {
			case "tail_tokens":
				cfg.Recovery.TailTokens = parsed
			case "tail_bytes_per_token":
				cfg.Recovery.TailBytesPerToken = parsed
			case "tail_skip_summaries":
				cfg.Recovery.TailSkipSummaries = parsed
			}
		case "summary":
			switch key {
			case "chunk_tokens":
				cfg.Summary.ChunkTokens = parsed
			case "overlap_percent":
				cfg.Summary.OverlapPercent = parsed
			case "rollup_every_n_chunks":
				cfg.Summary.RollupEveryNChunks = parsed
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}
