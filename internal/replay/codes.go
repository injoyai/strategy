package replay

const (
	codeNameEmpty         = "replay.name_empty"
	codeRangeInvalid      = "replay.range_invalid"
	codeTimezoneInvalid   = "replay.timezone_invalid"
	codePolicyInvalid     = "replay.policy_invalid"
	codeModelInvalid      = "replay.model_invalid"
	codeCashInvalid       = "replay.cash_invalid"
	codeConditionsMissing = "replay.conditions_missing"
)

const (
	codeConfigInvalid    = "replay.config_invalid"
	codeSessionMismatch  = "replay.session_mismatch"
	codeRevisionConflict = "replay.revision_conflict"
	codeStateInvalid     = "replay.state_invalid"
	codeCommandKind      = "replay.command_kind"
	codeEndOfRange       = "replay.end_of_range"
	// codeQueryInvalid: a session-scoped read or screen command is malformed
	// (no dataset, no instruments, no computation version). It is refused
	// instead of being evaluated against a default nobody asked for.
	codeQueryInvalid = "replay.query_invalid"
	// codeFutureRead: a read reaches past the session decision time. The server
	// refuses it rather than clipping the window, because a clipped answer reads
	// like a complete one.
	codeFutureRead = "replay.future_read"
)
