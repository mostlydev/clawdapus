package schedule

import "time"

type StateFile struct {
	Version     int                        `json:"version"`
	Pod         string                     `json:"pod,omitempty"`
	UpdatedAt   *time.Time                 `json:"updated_at,omitempty"`
	Invocations map[string]InvocationState `json:"invocations,omitempty"`
}

type InvocationState struct {
	Paused              bool       `json:"paused,omitempty"`
	PausedUntil         *time.Time `json:"paused_until,omitempty"`
	PauseReason         string     `json:"pause_reason,omitempty"`
	SkipNext            bool       `json:"skip_next,omitempty"`
	Degraded            bool       `json:"degraded,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures,omitempty"`
	LastEvaluatedAt     *time.Time `json:"last_evaluated_at,omitempty"`
	LastAttemptedAt     *time.Time `json:"last_attempted_at,omitempty"`
	LastFiredAt         *time.Time `json:"last_fired_at,omitempty"`
	LastSkippedAt       *time.Time `json:"last_skipped_at,omitempty"`
	NextFireAt          *time.Time `json:"next_fire_at,omitempty"`
	LastStatus          string     `json:"last_status,omitempty"`
	LastDetail          string     `json:"last_detail,omitempty"`
}

func (s StateFile) Clone() StateFile {
	out := StateFile{
		Version: s.Version,
		Pod:     s.Pod,
	}
	if s.UpdatedAt != nil {
		ts := *s.UpdatedAt
		out.UpdatedAt = &ts
	}
	if len(s.Invocations) > 0 {
		out.Invocations = make(map[string]InvocationState, len(s.Invocations))
		for id, state := range s.Invocations {
			out.Invocations[id] = state.Clone()
		}
	}
	return out
}

func (s InvocationState) Clone() InvocationState {
	return InvocationState{
		Paused:              s.Paused,
		PausedUntil:         cloneTimePtr(s.PausedUntil),
		PauseReason:         s.PauseReason,
		SkipNext:            s.SkipNext,
		Degraded:            s.Degraded,
		ConsecutiveFailures: s.ConsecutiveFailures,
		LastEvaluatedAt:     cloneTimePtr(s.LastEvaluatedAt),
		LastAttemptedAt:     cloneTimePtr(s.LastAttemptedAt),
		LastFiredAt:         cloneTimePtr(s.LastFiredAt),
		LastSkippedAt:       cloneTimePtr(s.LastSkippedAt),
		NextFireAt:          cloneTimePtr(s.NextFireAt),
		LastStatus:          s.LastStatus,
		LastDetail:          s.LastDetail,
	}
}

func cloneTimePtr(ts *time.Time) *time.Time {
	if ts == nil {
		return nil
	}
	copy := *ts
	return &copy
}
