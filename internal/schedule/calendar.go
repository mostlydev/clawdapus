package schedule

import (
	"embed"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed calendars/*.json
var calendarFS embed.FS

type Calendar struct {
	Name      string
	Timezone  string
	location  *time.Location
	weekdays  map[time.Weekday]struct{}
	sessions  map[Session]timeWindow
	holidays  map[string]struct{}
	overrides map[string]calendarOverride
}

type calendarOverride struct {
	Closed   bool
	Sessions map[Session]timeWindow
}

type timeWindow struct {
	StartMin int
	EndMin   int
}

type rawCalendar struct {
	Name      string                   `json:"name"`
	Timezone  string                   `json:"timezone"`
	Weekdays  []int                    `json:"weekdays"`
	Sessions  map[string]rawTimeWindow `json:"sessions"`
	Holidays  []string                 `json:"holidays"`
	Overrides map[string]rawOverride   `json:"overrides"`
}

type rawOverride struct {
	Closed   bool                     `json:"closed,omitempty"`
	Sessions map[string]rawTimeWindow `json:"sessions,omitempty"`
}

type rawTimeWindow struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

var calendarRegistry = mustLoadCalendars()

func LookupCalendar(name string) (*Calendar, error) {
	name = strings.TrimSpace(name)
	cal, ok := calendarRegistry[name]
	if !ok {
		return nil, fmt.Errorf("unknown calendar %q (supported: %s)", name, strings.Join(KnownCalendars(), ", "))
	}
	return cal, nil
}

func (c *Calendar) SupportsSession(session Session) bool {
	if c == nil {
		return false
	}
	if session == SessionAnyOpen {
		return len(c.sessions) > 0
	}
	_, ok := c.sessions[session]
	return ok
}

func (c *Calendar) SessionAt(t time.Time) SessionState {
	if c == nil {
		return SessionState{Name: SessionClosed, Reason: "calendar-missing"}
	}

	local := t.In(c.location)
	dateKey := local.Format("2006-01-02")
	if override, ok := c.overrides[dateKey]; ok {
		if override.Closed {
			return SessionState{Name: SessionClosed, Timezone: c.Timezone, Reason: "override-closed"}
		}
		if state, ok := sessionStateAt(local, c.Timezone, override.Sessions); ok {
			return state
		}
		return SessionState{Name: SessionClosed, Timezone: c.Timezone, Reason: "override-closed"}
	}

	if _, holiday := c.holidays[dateKey]; holiday {
		return SessionState{Name: SessionClosed, Timezone: c.Timezone, Reason: "holiday"}
	}
	if _, ok := c.weekdays[local.Weekday()]; !ok {
		return SessionState{Name: SessionClosed, Timezone: c.Timezone, Reason: "weekday-closed"}
	}
	if state, ok := sessionStateAt(local, c.Timezone, c.sessions); ok {
		return state
	}
	return SessionState{Name: SessionClosed, Timezone: c.Timezone, Reason: "session-closed"}
}

func sessionStateAt(local time.Time, timezone string, sessions map[Session]timeWindow) (SessionState, bool) {
	minuteOfDay := local.Hour()*60 + local.Minute()
	for _, session := range []Session{SessionPreMarket, SessionRegular, SessionAfterHours} {
		window, ok := sessions[session]
		if !ok {
			continue
		}
		if window.Contains(minuteOfDay) {
			return SessionState{Open: true, Name: session, Timezone: timezone}, true
		}
	}
	return SessionState{}, false
}

func (w timeWindow) Contains(minuteOfDay int) bool {
	if w.StartMin == 0 && w.EndMin == 1440 {
		return minuteOfDay >= 0 && minuteOfDay < 1440
	}
	if w.EndMin > w.StartMin {
		return minuteOfDay >= w.StartMin && minuteOfDay < w.EndMin
	}
	if w.EndMin < w.StartMin {
		return minuteOfDay >= w.StartMin || minuteOfDay < w.EndMin
	}
	return false
}

func mustLoadCalendars() map[string]*Calendar {
	entries, err := calendarFS.ReadDir("calendars")
	if err != nil {
		panic(fmt.Errorf("schedule calendars: read embedded dir: %w", err))
	}

	out := make(map[string]*Calendar, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		raw, err := calendarFS.ReadFile(filepath.Join("calendars", entry.Name()))
		if err != nil {
			panic(fmt.Errorf("schedule calendars: read %s: %w", entry.Name(), err))
		}
		cal, err := parseCalendar(raw)
		if err != nil {
			panic(fmt.Errorf("schedule calendars: parse %s: %w", entry.Name(), err))
		}
		out[cal.Name] = cal
	}
	return out
}

func parseCalendar(raw []byte) (*Calendar, error) {
	var input rawCalendar
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}

	name := strings.TrimSpace(input.Name)
	if name == "" {
		return nil, fmt.Errorf("calendar name must not be empty")
	}
	locationName := strings.TrimSpace(input.Timezone)
	if locationName == "" {
		return nil, fmt.Errorf("calendar %q: timezone must not be empty", name)
	}
	location, err := time.LoadLocation(locationName)
	if err != nil {
		return nil, fmt.Errorf("calendar %q: invalid timezone %q: %w", name, locationName, err)
	}

	weekdays := make(map[time.Weekday]struct{}, len(input.Weekdays))
	for _, value := range input.Weekdays {
		if value < 0 || value > 6 {
			return nil, fmt.Errorf("calendar %q: invalid weekday %d", name, value)
		}
		weekdays[time.Weekday(value)] = struct{}{}
	}

	sessions := make(map[Session]timeWindow, len(input.Sessions))
	for rawSession, window := range input.Sessions {
		session, ok := normalizeSession(rawSession)
		if !ok || session == SessionAnyOpen {
			return nil, fmt.Errorf("calendar %q: invalid session %q", name, rawSession)
		}
		parsed, err := parseWindow(window)
		if err != nil {
			return nil, fmt.Errorf("calendar %q session %q: %w", name, rawSession, err)
		}
		sessions[session] = parsed
	}
	if len(sessions) == 0 {
		return nil, fmt.Errorf("calendar %q: at least one session is required", name)
	}

	holidays := make(map[string]struct{}, len(input.Holidays))
	for _, date := range input.Holidays {
		date = strings.TrimSpace(date)
		if _, err := time.Parse("2006-01-02", date); err != nil {
			return nil, fmt.Errorf("calendar %q: invalid holiday %q: %w", name, date, err)
		}
		holidays[date] = struct{}{}
	}

	overrides := make(map[string]calendarOverride, len(input.Overrides))
	for date, rawOverride := range input.Overrides {
		if _, err := time.Parse("2006-01-02", date); err != nil {
			return nil, fmt.Errorf("calendar %q: invalid override date %q: %w", name, date, err)
		}
		override := calendarOverride{Closed: rawOverride.Closed}
		if len(rawOverride.Sessions) > 0 {
			override.Sessions = make(map[Session]timeWindow, len(rawOverride.Sessions))
			for rawSession, window := range rawOverride.Sessions {
				session, ok := normalizeSession(rawSession)
				if !ok || session == SessionAnyOpen {
					return nil, fmt.Errorf("calendar %q override %q: invalid session %q", name, date, rawSession)
				}
				parsed, err := parseWindow(window)
				if err != nil {
					return nil, fmt.Errorf("calendar %q override %q session %q: %w", name, date, rawSession, err)
				}
				override.Sessions[session] = parsed
			}
		}
		overrides[date] = override
	}

	return &Calendar{
		Name:      name,
		Timezone:  locationName,
		location:  location,
		weekdays:  weekdays,
		sessions:  sessions,
		holidays:  holidays,
		overrides: overrides,
	}, nil
}

func parseWindow(input rawTimeWindow) (timeWindow, error) {
	start, err := parseClock(input.Start)
	if err != nil {
		return timeWindow{}, fmt.Errorf("start: %w", err)
	}
	end, err := parseClock(input.End)
	if err != nil {
		return timeWindow{}, fmt.Errorf("end: %w", err)
	}
	if start == end {
		return timeWindow{}, fmt.Errorf("start and end must differ")
	}
	return timeWindow{StartMin: start, EndMin: end}, nil
}

func parseClock(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	parts := strings.Split(raw, ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid clock %q", raw)
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("invalid hour in %q", raw)
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, fmt.Errorf("invalid minute in %q", raw)
	}
	if hour == 24 && minute == 0 {
		return 1440, nil
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, fmt.Errorf("invalid clock %q", raw)
	}
	return hour*60 + minute, nil
}

func sortedCalendarNames() []string {
	names := make([]string, 0, len(calendarRegistry))
	for name := range calendarRegistry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
