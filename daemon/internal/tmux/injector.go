package tmux

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	logpkg "github.com/norm/relay-daemon/internal/log"
	"github.com/norm/relay-daemon/pkg/envelope"
)

// Injector maps envelopes to tmux targets and handles prompt-aware queuing.
type Injector struct {
	tmux         *Tmux
	targets      map[string]string
	promptGating string
	queueMaxAge  time.Duration
	logger       *logpkg.EventLog

	mu        sync.Mutex
	queues    map[string]*paneQueue
	startOnce sync.Once
}

type queuedMessage struct {
	env      *envelope.Envelope
	enqueued time.Time
	backoff  time.Duration
}

type paneQueue struct {
	target string
	paneID string

	mu     sync.Mutex
	items  []*queuedMessage
	notify chan struct{}
}

func NewInjector(tmux *Tmux, targets map[string]string) *Injector {
	return &Injector{
		tmux:         tmux,
		targets:      targets,
		promptGating: "all",
		queueMaxAge:  5 * time.Minute,
		queues:       make(map[string]*paneQueue),
	}
}

func (i *Injector) SetLogger(logger *logpkg.EventLog) {
	i.logger = logger
}

func (i *Injector) SetPromptGating(mode string) {
	if mode == "" {
		return
	}
	i.promptGating = strings.ToLower(mode)
}

func (i *Injector) SetQueueMaxAge(maxAge time.Duration) {
	if maxAge <= 0 {
		return
	}
	i.queueMaxAge = maxAge
}

func (i *Injector) Start(ctx context.Context) {
	i.startOnce.Do(func() {
		i.mu.Lock()
		defer i.mu.Unlock()
		for target, pane := range i.targets {
			pq := newPaneQueue(target, pane)
			i.queues[target] = pq
			go pq.run(ctx, i)
		}
	})
}

func (i *Injector) Inject(env *envelope.Envelope) error {
	if env == nil {
		return fmt.Errorf("inject: nil envelope")
	}
	if err := env.Validate(); err != nil {
		return fmt.Errorf("inject: invalid envelope: %w", err)
	}
	target, ok := i.targets[env.To]
	if !ok {
		return fmt.Errorf("inject: unknown target %q", env.To)
	}

	item := &queuedMessage{env: env, enqueued: time.Now()}
	pq := i.getQueue(env.To, target)
	pq.enqueue(item)
	i.logEvent(logpkg.EventTypeEnqueue, env.From, env.To, env.MsgID, "")
	return nil
}

func (i *Injector) getQueue(target, paneID string) *paneQueue {
	i.mu.Lock()
	defer i.mu.Unlock()
	if pq, ok := i.queues[target]; ok {
		return pq
	}
	pq := newPaneQueue(target, paneID)
	i.queues[target] = pq
	return pq
}

func newPaneQueue(target, paneID string) *paneQueue {
	return &paneQueue{
		target: target,
		paneID: paneID,
		notify: make(chan struct{}, 1),
	}
}

func (pq *paneQueue) enqueue(item *queuedMessage) {
	pq.mu.Lock()
	pq.items = append(pq.items, item)
	pq.mu.Unlock()
	select {
	case pq.notify <- struct{}{}:
	default:
	}
}

func (pq *paneQueue) dequeue() *queuedMessage {
	pq.mu.Lock()
	defer pq.mu.Unlock()
	if len(pq.items) == 0 {
		return nil
	}
	item := pq.items[0]
	pq.items = pq.items[1:]
	return item
}

func (pq *paneQueue) requeueFront(item *queuedMessage) {
	pq.mu.Lock()
	pq.items = append([]*queuedMessage{item}, pq.items...)
	pq.mu.Unlock()
}

func (pq *paneQueue) run(ctx context.Context, injector *Injector) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		item := pq.dequeue()
		if item == nil {
			select {
			case <-pq.notify:
				continue
			case <-ctx.Done():
				return
			}
		}

		if injector.queueMaxAge > 0 && time.Since(item.enqueued) > injector.queueMaxAge {
			injector.logEvent("drop", item.env.From, pq.target, item.env.MsgID, truncateForLog(item.env.Payload))
			continue
		}

		injector.logEvent(logpkg.EventTypeDequeue, item.env.From, pq.target, item.env.MsgID, "")

		// Slash commands are injected bare so Claude Code parses them as skill invocations
		if strings.HasPrefix(strings.TrimSpace(item.env.Payload), "/") {
			if err := injector.tmux.SendToPane(pq.paneID, strings.TrimSpace(item.env.Payload)); err != nil {
				injector.logEvent(logpkg.EventTypeBlocked, item.env.From, pq.target, item.env.MsgID, truncateForLog(err.Error()))
				item.backoff = nextBackoff(item.backoff)
				pq.requeueFront(item)
				if !sleepOrDone(ctx, item.backoff) {
					return
				}
				continue
			}
			injector.logEvent(logpkg.EventTypeInject, item.env.From, pq.target, item.env.MsgID, "")
			continue
		}

		ready, tail, err := injector.IsPaneReady(pq.paneID, pq.target)
		if err != nil || !ready {
			if tail == "" && err != nil {
				tail = err.Error()
			}
			injector.logEvent(logpkg.EventTypeBlocked, item.env.From, pq.target, item.env.MsgID, truncateForLog(tail))
			item.backoff = nextBackoff(item.backoff)
			pq.requeueFront(item)
			if !sleepOrDone(ctx, item.backoff) {
				return
			}
			continue
		}

		// Wrap payload in relay-message XML tags for agent protocol.
		// Escape payload to prevent XML injection (& -> &amp;, < -> &lt;).
		safePayload := xmlEscapePayload(item.env.Payload)
		tagged := fmt.Sprintf("<relay-message from=%q to=%q kind=%q>\n[Relay from %s. Not from the human user.]\n\n%s\n</relay-message>",
			item.env.From, item.env.To, item.env.Kind, item.env.From, safePayload)

		if err := injector.tmux.SendToPane(pq.paneID, tagged); err != nil {
			injector.logEvent(logpkg.EventTypeBlocked, item.env.From, pq.target, item.env.MsgID, truncateForLog(err.Error()))
			item.backoff = nextBackoff(item.backoff)
			pq.requeueFront(item)
			if !sleepOrDone(ctx, item.backoff) {
				return
			}
			continue
		}

		injector.logEvent(logpkg.EventTypeInject, item.env.From, pq.target, item.env.MsgID, "")
	}
}

func (i *Injector) shouldGate(target string) bool {
	// Admin pane runs Claude, not a shell — never gate admin commands
	if target == "admin" {
		return false
	}
	switch i.promptGating {
	case "none":
		return false
	case "oc":
		return target == "oc"
	default:
		return true
	}
}

// IsPaneReady checks copy-mode and prompt readiness for a pane.
func (i *Injector) IsPaneReady(paneID, target string) (bool, string, error) {
	if !i.shouldGate(target) {
		return true, "", nil
	}

	mode, err := i.tmux.Run("display-message", "-t", paneID, "-p", "#{pane_mode}")
	if err != nil {
		return false, "", err
	}
	if strings.Contains(strings.ToLower(mode), "copy") {
		return false, "", nil
	}

	out, err := i.tmux.Run("capture-pane", "-t", paneID, "-p", "-S", "-5")
	if err != nil {
		return false, "", err
	}

	last := lastNonEmptyLine(out)
	if last == "" {
		return false, strings.TrimSpace(out), nil
	}

	for _, prefix := range []string{"❯", "›", "⏵", "?", "$", "%", ">"} {
		if strings.HasPrefix(strings.TrimSpace(last), prefix) {
			return true, strings.TrimSpace(out), nil
		}
	}
	return false, strings.TrimSpace(out), nil
}

func lastNonEmptyLine(out string) string {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			return line
		}
	}
	return ""
}

func nextBackoff(current time.Duration) time.Duration {
	if current <= 0 {
		return 250 * time.Millisecond
	}
	next := current * 2
	if next < 2*time.Second {
		return next
	}
	if next < 5*time.Second {
		return 5 * time.Second
	}
	return 5 * time.Second
}

func sleepOrDone(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// xmlEscapePayload escapes & and < in payload to prevent breaking the
// enclosing <relay-message> XML tags. We only escape these two characters
// to keep the payload readable for agents while preventing XML injection.
func xmlEscapePayload(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	return s
}

func truncateForLog(text string) string {
	const max = 200
	trimmed := strings.TrimSpace(text)
	if len(trimmed) <= max {
		return trimmed
	}
	return trimmed[len(trimmed)-max:]
}

func (i *Injector) logEvent(eventType, from, to, msgID, errText string) {
	if i.logger == nil {
		return
	}
	evt := logpkg.NewEvent(eventType, from, to).WithMsgID(msgID)
	if errText != "" {
		evt = evt.WithError(errText)
	}
	_ = i.logger.Log(evt)
}
