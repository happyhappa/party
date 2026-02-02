package inbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// LoadOffsets reads offsets from disk.
func LoadOffsets(path string) (map[string]int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]int64), nil
		}
		return nil, err
	}

	var offsets map[string]int64
	if err := json.Unmarshal(data, &offsets); err != nil {
		return nil, fmt.Errorf("decode offsets: %w", err)
	}
	if offsets == nil {
		offsets = make(map[string]int64)
	}
	return offsets, nil
}

func saveOffsets(path string, offsets map[string]int64) error {
	data, err := json.MarshalIndent(offsets, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
