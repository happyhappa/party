package supervisor

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	logpkg "github.com/norm/relay-daemon/internal/log"
	tmuxpkg "github.com/norm/relay-daemon/internal/tmux"
)

// PaneTailer captures and rotates pane output for debugging.
type PaneTailer struct {
	tmux      *tmuxpkg.Tmux
	paneMap   map[string]string
	lines     int
	rotations int
	outDir    string
	interval  time.Duration
	logger    *logpkg.EventLog
	hashes    map[string]string
}

func NewPaneTailer(tmux *tmuxpkg.Tmux, paneMap map[string]string, lines, rotations int, outDir string, interval time.Duration, logger *logpkg.EventLog) *PaneTailer {
	return &PaneTailer{
		tmux:      tmux,
		paneMap:   paneMap,
		lines:     lines,
		rotations: rotations,
		outDir:    outDir,
		interval:  interval,
		logger:    logger,
		hashes:    make(map[string]string),
	}
}

func (p *PaneTailer) Start(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.captureAll()
		}
	}
}

func (p *PaneTailer) captureAll() {
	if err := os.MkdirAll(p.outDir, 0o755); err != nil {
		log.Printf("pane tailer mkdir: %v", err)
		return
	}

	keys := make([]string, 0, len(p.paneMap))
	for key := range p.paneMap {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, name := range keys {
		pane := p.paneMap[name]
		if err := p.capturePane(strings.ToLower(name), pane); err != nil {
			_ = p.logger.Log(logpkg.Event{Kind: "pane_tail_error", Target: name, Error: err.Error()})
			continue
		}
	}
}

func (p *PaneTailer) capturePane(name, pane string) error {
	if pane == "" {
		return fmt.Errorf("pane tailer: empty pane for %s", name)
	}

	lines := p.lines
	if lines <= 0 {
		lines = 150
	}
	output, err := p.tmuxCapture(pane, lines)
	if err != nil {
		return err
	}

	hash := md5.Sum([]byte(output))
	hashStr := hex.EncodeToString(hash[:])
	if prev, ok := p.hashes[name]; ok && prev == hashStr {
		return nil
	}

	path := filepath.Join(p.outDir, name+".txt")
	if err := rotate(path, p.rotations); err != nil {
		return err
	}

	if err := writeAtomic(path, []byte(output)); err != nil {
		return err
	}

	p.hashes[name] = hashStr
	return nil
}

func (p *PaneTailer) tmuxCapture(pane string, lines int) (string, error) {
	start := -lines
	out, err := p.tmux.Run("capture-pane", "-t", pane, "-p", "-S", strconv.Itoa(start))
	if err != nil {
		return "", err
	}
	return out + "\n", nil
}

func rotate(path string, max int) error {
	if max <= 0 {
		return nil
	}
	oldest := fmt.Sprintf("%s.%d", path, max)
	_ = os.Remove(oldest)
	for i := max - 1; i >= 1; i-- {
		from := fmt.Sprintf("%s.%d", path, i)
		to := fmt.Sprintf("%s.%d", path, i+1)
		if _, err := os.Stat(from); err == nil {
			if err := os.Rename(from, to); err != nil {
				return err
			}
		}
	}
	if _, err := os.Stat(path); err == nil {
		if err := os.Rename(path, fmt.Sprintf("%s.1", path)); err != nil {
			return err
		}
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
