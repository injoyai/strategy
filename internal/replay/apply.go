package replay

import (
	"time"

	"github.com/injoyai/strategy/internal/domain"
)

// Init creates a session in awaiting_action at the first server-computed
// decision point on or after cfg.From. The start decision time always comes
// from the server's clock/calendar, never from a caller.
func Init(id domain.ID, cfg Config, clk DecisionClock) (Session, error) {
	if issues := cfg.Validate(); len(issues) > 0 {
		return Session{}, domain.NewError(codeConfigInvalid, "replay session config is invalid")
	}
	start, ok := clk.AtOrAfter(cfg.From)
	if !ok || start.After(cfg.To) {
		return Session{}, domain.NewError(codeEndOfRange, "no decision point within [%s,%s)", cfg.From.UTC().Format(time.RFC3339), cfg.To.UTC().Format(time.RFC3339))
	}
	if !CanTransition(StateInitializing, StateAwaitingAction) {
		return Session{}, domain.NewError(codeStateInvalid, "cannot open a session")
	}
	return Session{ID: id, ConfigHash: cfg.Hash(), State: StateAwaitingAction, CurrentAsOf: start, Revision: 0, ParentID: cfg.ParentID}, nil
}

func (s Session) gate(cmd Command) error {
	if cmd.SessionID != s.ID {
		return domain.NewError(codeSessionMismatch, "command targets session %q but session is %q", cmd.SessionID, s.ID)
	}
	if cmd.ExpectedRevision != s.Revision {
		return domain.NewError(codeRevisionConflict, "expected revision %d but session is at %d", cmd.ExpectedRevision, s.Revision)
	}
	return nil
}

// ApplyOrder records an order or cancel intent at the current decision point.
// It is only legal while awaiting_action; the historical decision time equals
// the session decision time and is never taken from the command.
func ApplyOrder(s Session, cmd Command) (Session, error) {
	if err := s.gate(cmd); err != nil {
		return s, err
	}
	if cmd.Kind != CmdSubmitOrder && cmd.Kind != CmdCancelOrder {
		return s, domain.NewError(codeCommandKind, "unsupported order command %q", cmd.Kind)
	}
	if s.State != StateAwaitingAction {
		return s, domain.NewError(codeStateInvalid, "orders require awaiting_action, session is %q", s.State)
	}
	return Session{ID: s.ID, ConfigHash: s.ConfigHash, State: StateAwaitingAction,
		CurrentAsOf: s.CurrentAsOf, Revision: s.Revision + 1, ParentID: s.ParentID}, nil
}

// OpenAdvance begins advancing: awaiting_action -> advancing, locking the
// session against further orders/cancels/advance. It returns the server-computed
// target decision point (next day) without yet publishing as_of or revision.
func OpenAdvance(s Session, cmd Command, clk DecisionClock) (Session, time.Time, error) {
	if err := s.gate(cmd); err != nil {
		return s, time.Time{}, err
	}
	if cmd.Kind != CmdAdvance {
		return s, time.Time{}, domain.NewError(codeCommandKind, "unsupported advance command %q", cmd.Kind)
	}
	if s.State != StateAwaitingAction {
		return s, time.Time{}, domain.NewError(codeStateInvalid, "advance requires awaiting_action, session is %q", s.State)
	}
	next, ok := clk.NextAfter(s.CurrentAsOf)
	if !ok {
		return s, time.Time{}, domain.NewError(codeEndOfRange, "no further decision point after %s", s.CurrentAsOf.UTC().Format(time.RFC3339))
	}
	return Session{ID: s.ID, ConfigHash: s.ConfigHash, State: StateAdvancing,
		CurrentAsOf: s.CurrentAsOf, Revision: s.Revision, ParentID: s.ParentID}, next, nil
}

// CommitAdvance finalises a committed daily step: advancing -> awaiting_action
// at the published next decision point, incrementing the revision atomically.
func CommitAdvance(s Session, cmd Command, to time.Time) (Session, error) {
	if err := s.gate(cmd); err != nil {
		return s, err
	}
	if s.State != StateAdvancing {
		return s, domain.NewError(codeStateInvalid, "commit requires advancing, session is %q", s.State)
	}
	return Session{ID: s.ID, ConfigHash: s.ConfigHash, State: StateAwaitingAction,
		CurrentAsOf: to, Revision: s.Revision + 1, ParentID: s.ParentID}, nil
}

// OpenClose begins closing: awaiting_action -> closing, rejecting new orders.
func OpenClose(s Session, cmd Command) (Session, error) {
	if err := s.gate(cmd); err != nil {
		return s, err
	}
	if cmd.Kind != CmdClose {
		return s, domain.NewError(codeCommandKind, "unsupported close command %q", cmd.Kind)
	}
	if s.State != StateAwaitingAction {
		return s, domain.NewError(codeStateInvalid, "close requires awaiting_action, session is %q", s.State)
	}
	return Session{ID: s.ID, ConfigHash: s.ConfigHash, State: StateClosing,
		CurrentAsOf: s.CurrentAsOf, Revision: s.Revision, ParentID: s.ParentID}, nil
}

// CommitClose finalises completion: closing -> completed.
func CommitClose(s Session, cmd Command, reason string) (Session, error) {
	if err := s.gate(cmd); err != nil {
		return s, err
	}
	if s.State != StateClosing {
		return s, domain.NewError(codeStateInvalid, "commit requires closing, session is %q", s.State)
	}
	return Session{ID: s.ID, ConfigHash: s.ConfigHash, State: StateCompleted,
		CurrentAsOf: s.CurrentAsOf, Revision: s.Revision + 1, EndReason: reason, ParentID: s.ParentID}, nil
}

// Fail marks an unrecoverable session failed from any non-terminal state.
func Fail(s Session) (Session, error) {
	if s.State == StateCompleted || s.State == StateFailed {
		return s, domain.NewError(codeStateInvalid, "session %q already in terminal state %q", s.ID, s.State)
	}
	return Session{ID: s.ID, ConfigHash: s.ConfigHash, State: StateFailed,
		CurrentAsOf: s.CurrentAsOf, Revision: s.Revision, EndReason: "failed", ParentID: s.ParentID}, nil
}
