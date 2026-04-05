package schedule

import (
	"fmt"
	"sort"
	"strings"
)

type Session string

const (
	SessionClosed     Session = "closed"
	SessionAnyOpen    Session = "any-open"
	SessionRegular    Session = "regular"
	SessionPreMarket  Session = "pre-market"
	SessionAfterHours Session = "after-hours"
)

type When struct {
	Calendar string  `json:"calendar,omitempty" yaml:"calendar,omitempty"`
	Session  Session `json:"session,omitempty" yaml:"session,omitempty"`
}

type SessionState struct {
	Open     bool    `json:"open"`
	Name     Session `json:"name"`
	Timezone string  `json:"timezone,omitempty"`
	Reason   string  `json:"reason,omitempty"`
}

func (w *When) Validate() error {
	if w == nil {
		return nil
	}
	if strings.TrimSpace(w.Calendar) == "" {
		return fmt.Errorf("when.calendar must not be empty")
	}
	cal, err := LookupCalendar(w.Calendar)
	if err != nil {
		return err
	}
	session := w.SessionOrDefault()
	if session == SessionAnyOpen {
		return nil
	}
	if !cal.SupportsSession(session) {
		return fmt.Errorf("calendar %q does not support session %q", cal.Name, session)
	}
	return nil
}

func (w *When) SessionOrDefault() Session {
	if w == nil {
		return SessionAnyOpen
	}
	if normalized, ok := normalizeSession(string(w.Session)); ok {
		return normalized
	}
	return SessionAnyOpen
}

func (w *When) Clone() *When {
	if w == nil {
		return nil
	}
	copy := *w
	copy.Calendar = strings.TrimSpace(copy.Calendar)
	copy.Session = copy.SessionOrDefault()
	return &copy
}

func (s SessionState) Matches(required Session) bool {
	switch required {
	case SessionAnyOpen:
		return s.Open
	case SessionRegular, SessionPreMarket, SessionAfterHours:
		return s.Open && s.Name == required
	default:
		return false
	}
}

func ParseSession(raw string) (Session, error) {
	session, ok := normalizeSession(raw)
	if !ok {
		return "", fmt.Errorf("unsupported session %q (supported: %s)", raw, strings.Join(KnownSessions(), ", "))
	}
	return session, nil
}

func KnownSessions() []string {
	return []string{
		string(SessionAnyOpen),
		string(SessionRegular),
		string(SessionPreMarket),
		string(SessionAfterHours),
	}
}

func normalizeSession(raw string) (Session, bool) {
	switch Session(strings.ToLower(strings.TrimSpace(raw))) {
	case "":
		return SessionAnyOpen, true
	case SessionAnyOpen:
		return SessionAnyOpen, true
	case SessionRegular:
		return SessionRegular, true
	case SessionPreMarket:
		return SessionPreMarket, true
	case SessionAfterHours:
		return SessionAfterHours, true
	default:
		return "", false
	}
}

func KnownCalendars() []string {
	out := make([]string, 0, len(calendarRegistry))
	for name := range calendarRegistry {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
