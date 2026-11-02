package oxgen

import (
	"context"
	"strings"
)

type inputLine struct {
	text string
	err  error
}
type inputWatch struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// Keep one pending read across prompt/operation boundaries. A cancelled watcher
// must not leave a competing reader that consumes the next menu choice.
func (u *UI) readLine(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if u.pending == nil {
		ready := make(chan inputLine, 1)
		u.pending = ready
		go func() { text, err := u.reader.ReadString('\n'); ready <- inputLine{text, err} }()
	}
	select {
	case line := <-u.pending:
		u.pending = nil
		return line.text, line.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (u *UI) startWatch() {
	if !u.Terminal || u.operation == nil || u.operation.Err() != nil || u.watch != nil {
		return
	}
	ctx, cancel := context.WithCancel(u.operation)
	w := &inputWatch{cancel: cancel, done: make(chan struct{})}
	u.watch = w
	go func() {
		defer close(w.done)
		text, err := u.readLine(ctx)
		if err != nil && text == "" {
			return
		}
		if strings.TrimSpace(text) == "0" {
			u.cancelled.Store(true)
			u.operationCancel()
			return
		}
		// Preserve a non-cancellation line for the next explicit prompt.
		u.pending = make(chan inputLine, 1)
		u.pending <- inputLine{text, err}
	}()
}

func (u *UI) stopWatch() {
	if u.watch == nil {
		return
	}
	w := u.watch
	w.cancel()
	<-w.done
	u.watch = nil
}

func (u *UI) Run(ctx context.Context, watch bool, action func(context.Context)) {
	op, cancel := context.WithCancel(ctx)
	u.operation, u.operationCancel = op, cancel
	if watch {
		u.startWatch()
	}
	defer func() { u.stopWatch(); cancel(); u.operation, u.operationCancel = nil, nil }()
	action(op)
}
