package app

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/worxbend/twi/internal/automation"
)

// ChatRecorder receives observed events only. The caller owns its lifetime and
// final durable flush. Ready and Lost keep coverage aligned with transport health.
type ChatRecorder interface {
	Submit(automation.Record) error
	Errors() <-chan error
	Ready(context.Context, string, time.Time) error
	Lost(context.Context, time.Time) error
}

type recorderErrorMsg struct{ err error }

func (m shellModel) nextRecorderErrorCommand() tea.Cmd {
	if m.recorder == nil || m.recorderError != nil {
		return nil
	}
	failures := m.recorder.Errors()
	lifetime := m.lifetimeContext()
	return func() tea.Msg {
		select {
		case err, ok := <-failures:
			if !ok {
				return nil
			}
			return recorderErrorMsg{err: err}
		case <-lifetime.Done():
			return nil
		}
	}
}
