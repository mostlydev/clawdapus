package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	schedulepkg "github.com/mostlydev/clawdapus/internal/schedule"
	"github.com/robfig/cron/v3"
)

type schedulePageData struct {
	PodName         string
	ActiveTab       string
	HasSchedule     bool
	HasAgentContext bool
	Summary         []dashStat
	Cards           []scheduleCard
	HasCards        bool
	Notice          string
	Error           string
	HasNotice       bool
	HasError        bool
	HasStatusErrors bool
}

type scheduleCard struct {
	ID            string
	Name          string
	ServiceAgent  string
	StateAccent   string
	LifecyclePill scheduleBadge
	HealthPill    *scheduleBadge
	SkipNextChip  *scheduleBadge
	NextSlot      nextSlotDisplay
	LastEvent     lastEventDisplay
	Details       scheduleDetails
	Primary       schedulePrimaryAction
	Overflow      []scheduleAction
	BypassFire    scheduleAction
	PauseForm     pauseFormFields
	SortKey       time.Time
	Pinned        bool
}

type scheduleBadge struct {
	Label string
	Tone  string
}

type nextSlotDisplay struct {
	HeroLabel     string
	SlotRelative  string
	SlotAbsolute  string
	CronExpr      string
	GateSubtitle  string
	Modifier      string
	FollowupLabel string
	FollowupValue string
	Notes         []string
	Dimmed        bool
}

type lastEventDisplay struct {
	StatusLabel  string
	StatusTone   string
	Relative     string
	Absolute     string
	Detail       string
	Tooltip      string
	HasTimestamp bool
}

type scheduleDetails struct {
	ID          string
	Service     string
	AgentID     string
	Message     string
	Target      string
	WakeAdapter string
	WakeTarget  string
	WakeCommand string
}

type schedulePrimaryAction struct {
	Label            string
	ActionPath       string
	ButtonClass      string
	TogglesPauseForm bool
}

type scheduleAction struct {
	Label       string
	ActionPath  string
	ButtonClass string
	Fields      []scheduleField
}

type scheduleField struct {
	Name  string
	Value string
}

type pauseFormFields struct {
	ActionPath string
	UntilLocal string
	Reason     string
	Timezone   string
}

func (h *handler) renderSchedule(w http.ResponseWriter, r *http.Request) {
	if !h.hasSchedule() {
		http.NotFound(w, r)
		return
	}

	invocations, err := h.scheduleSource.List(r.Context())
	now := time.Now().UTC()
	if h != nil && h.now != nil {
		now = h.now().UTC()
	}
	data := buildSchedulePageData(
		h.manifest.PodName,
		invocations,
		strings.TrimSpace(r.URL.Query().Get("notice")),
		firstNonEmpty(strings.TrimSpace(r.URL.Query().Get("error")), errString(err)),
		now,
	)
	data.HasAgentContext = h.hasAgentContext()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = h.tpl.ExecuteTemplate(w, "schedule.html", data)
}

func (h *handler) handleScheduleAction(w http.ResponseWriter, r *http.Request) {
	if !h.hasSchedule() {
		http.NotFound(w, r)
		return
	}
	id, action, ok := parseDashSchedulePath(r.URL.Path)
	if !ok || action == "" {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		redirectSchedule(w, r, "", "invalid form body")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var (
		err    error
		notice string
	)
	switch action {
	case "pause":
		until, parseErr := h.resolvePauseUntil(ctx, id, r.FormValue("until_local"), r.FormValue("until"))
		if parseErr != nil {
			redirectSchedule(w, r, "", parseErr.Error())
			return
		}
		err = h.scheduleSource.Pause(ctx, id, until, r.FormValue("reason"))
		notice = fmt.Sprintf("Paused %s.", id)
	case "resume":
		err = h.scheduleSource.Resume(ctx, id)
		notice = fmt.Sprintf("Resumed %s.", id)
	case "skip-next":
		err = h.scheduleSource.SkipNext(ctx, id)
		notice = fmt.Sprintf("Marked %s to skip the next fire.", id)
	case "clear-skip-next":
		err = h.scheduleSource.ClearSkipNext(ctx, id)
		notice = fmt.Sprintf("Cleared skip-next for %s.", id)
	case "fire":
		bypassWhen := formBool(r.FormValue("bypass_when"))
		bypassPause := formBool(r.FormValue("bypass_pause"))
		err = h.scheduleSource.Fire(ctx, id, bypassWhen, bypassPause)
		notice = fmt.Sprintf("Triggered %s.", id)
		if bypassWhen || bypassPause {
			notice = fmt.Sprintf("Force-fired %s.", id)
		}
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		redirectSchedule(w, r, "", err.Error())
		return
	}
	redirectSchedule(w, r, notice, "")
}

func (h *handler) resolvePauseUntil(ctx context.Context, id, localValue, fallback string) (string, error) {
	localValue = strings.TrimSpace(localValue)
	if localValue == "" {
		return strings.TrimSpace(fallback), nil
	}
	view, err := h.scheduleSource.Get(ctx, id)
	if err != nil {
		return "", err
	}
	return convertLocalPauseUntil(localValue, view.Timezone)
}

func buildSchedulePageData(podName string, invocations []scheduleInvocationView, notice, errMsg string, now time.Time) schedulePageData {
	cards := make([]scheduleCard, 0, len(invocations))
	paused := 0
	degraded := 0
	nextSlot := soonestScheduleSlot(invocations, now)

	for _, inv := range invocations {
		if inv.State.Paused {
			paused++
		}
		if inv.State.Degraded {
			degraded++
		}
		cards = append(cards, buildScheduleCard(inv, now))
	}

	sort.Slice(cards, func(i, j int) bool {
		return lessScheduleCard(cards[i], cards[j])
	})

	summary := []dashStat{
		{Label: "Scheduled", Value: fmt.Sprintf("%d", len(invocations)), Hint: "scheduler-owned jobs in scope", Tone: "neutral"},
		{Label: "Paused", Value: fmt.Sprintf("%d", paused), Hint: "manually paused right now", Tone: toneForCount(paused)},
		{Label: "Degraded", Value: fmt.Sprintf("%d", degraded), Hint: "throttled after repeated failures", Tone: toneForCount(degraded)},
		{Label: "Next slot", Value: firstNonEmpty(formatFutureRelative(nextSlot, now), "—"), Hint: "soonest non-paused cron slot", Tone: "neutral"},
	}

	return schedulePageData{
		PodName:     podName,
		ActiveTab:   "schedule",
		HasSchedule: true,
		Summary:     summary,
		Cards:       cards,
		HasCards:    len(cards) > 0,
		Notice:      notice,
		Error:       errMsg,
		HasNotice:   strings.TrimSpace(notice) != "",
		HasError:    strings.TrimSpace(errMsg) != "",
	}
}

func buildScheduleCard(inv scheduleInvocationView, now time.Time) scheduleCard {
	name := scheduleInvocationName(inv)
	pinned := inv.State.Paused || inv.State.Degraded

	card := scheduleCard{
		ID:            inv.ID,
		Name:          name,
		ServiceAgent:  strings.TrimSpace(inv.Service) + " \u2192 " + strings.TrimSpace(inv.AgentID),
		StateAccent:   scheduleAccentClass(inv.State),
		LifecyclePill: scheduleLifecyclePill(inv.State),
		HealthPill:    scheduleHealthPill(inv.State),
		SkipNextChip:  scheduleSkipNextChip(inv.State),
		NextSlot:      buildNextSlotDisplay(inv, now),
		LastEvent:     buildLastEventDisplay(inv.State, inv.Timezone, now),
		Details: scheduleDetails{
			ID:          inv.ID,
			Service:     strings.TrimSpace(inv.Service),
			AgentID:     strings.TrimSpace(inv.AgentID),
			Message:     firstNonEmpty(strings.TrimSpace(inv.Message), "—"),
			Target:      firstNonEmpty(strings.TrimSpace(inv.To), "direct wake"),
			WakeAdapter: firstNonEmpty(strings.TrimSpace(inv.Wake.Adapter), "—"),
			WakeTarget:  firstNonEmpty(strings.TrimSpace(inv.Wake.Target), "—"),
			WakeCommand: firstNonEmpty(strings.Join(inv.Wake.Command, " "), "—"),
		},
		Primary: schedulePrimaryAction{
			Label:            "Pause",
			ButtonClass:      "dash-button",
			TogglesPauseForm: true,
		},
		Overflow: []scheduleAction{
			buildSkipNextAction(inv),
			{
				Label:       "Fire now",
				ActionPath:  "/schedule/" + url.PathEscape(inv.ID) + "/fire",
				ButtonClass: "dash-menu-item",
			},
		},
		BypassFire: scheduleAction{
			Label:       "Force fire",
			ActionPath:  "/schedule/" + url.PathEscape(inv.ID) + "/fire",
			ButtonClass: "dash-button dash-button-danger",
			Fields: []scheduleField{
				{Name: "bypass_when", Value: "true"},
				{Name: "bypass_pause", Value: "true"},
			},
		},
		PauseForm: pauseFormFields{
			ActionPath: "/schedule/" + url.PathEscape(inv.ID) + "/pause",
			UntilLocal: formatLocalInputTime(inv.State.PausedUntil, inv.Timezone),
			Reason:     strings.TrimSpace(inv.State.PauseReason),
			Timezone:   firstNonEmpty(strings.TrimSpace(inv.Timezone), "UTC"),
		},
		Pinned: pinned,
	}

	if inv.State.NextFireAt != nil {
		card.SortKey = inv.State.NextFireAt.UTC()
	}
	if inv.State.Paused {
		card.Primary = schedulePrimaryAction{
			Label:       "Resume",
			ActionPath:  "/schedule/" + url.PathEscape(inv.ID) + "/resume",
			ButtonClass: "dash-button",
		}
	}

	return card
}

func lessScheduleCard(left, right scheduleCard) bool {
	if left.Pinned != right.Pinned {
		return left.Pinned
	}
	if left.SortKey.IsZero() != right.SortKey.IsZero() {
		return !left.SortKey.IsZero()
	}
	if !left.SortKey.Equal(right.SortKey) {
		return left.SortKey.Before(right.SortKey)
	}
	if left.Name != right.Name {
		return left.Name < right.Name
	}
	return left.ID < right.ID
}

func buildSkipNextAction(inv scheduleInvocationView) scheduleAction {
	action := scheduleAction{
		Label:       "Skip next",
		ActionPath:  "/schedule/" + url.PathEscape(inv.ID) + "/skip-next",
		ButtonClass: "dash-menu-item",
	}
	if inv.State.SkipNext {
		action.Label = "Clear skip-next"
		action.ActionPath = "/schedule/" + url.PathEscape(inv.ID) + "/clear-skip-next"
	}
	return action
}

func scheduleAccentClass(state schedulepkg.InvocationState) string {
	switch {
	case state.Degraded:
		return "dash-schedule-card-critical"
	case state.Paused:
		return "dash-schedule-card-warning"
	default:
		return ""
	}
}

func scheduleLifecyclePill(state schedulepkg.InvocationState) scheduleBadge {
	if state.Paused {
		return scheduleBadge{Label: "paused", Tone: "tone-warning"}
	}
	return scheduleBadge{Label: "scheduled", Tone: "tone-neutral"}
}

func scheduleHealthPill(state schedulepkg.InvocationState) *scheduleBadge {
	if !state.Degraded {
		return nil
	}
	return &scheduleBadge{Label: "degraded", Tone: "tone-critical"}
}

func scheduleSkipNextChip(state schedulepkg.InvocationState) *scheduleBadge {
	if !state.SkipNext {
		return nil
	}
	return &scheduleBadge{Label: "skip next", Tone: "tone-neutral"}
}

func buildNextSlotDisplay(inv scheduleInvocationView, now time.Time) nextSlotDisplay {
	display := nextSlotDisplay{
		HeroLabel:    "Next fire",
		SlotRelative: firstNonEmpty(formatFutureRelative(inv.State.NextFireAt, now), "not scheduled"),
		SlotAbsolute: formatScheduleTime(inv.State.NextFireAt, inv.Timezone),
		CronExpr:     firstNonEmpty(strings.TrimSpace(inv.Schedule), "—"),
		GateSubtitle: scheduleGateLabel(inv.When),
	}

	if inv.State.NextFireAt == nil {
		display.HeroLabel = "Next slot"
		return display
	}

	switch {
	case inv.State.Paused:
		display.HeroLabel = "Next slot"
		display.Modifier = "(paused, will be skipped)"
		display.Dimmed = true
		if inv.State.PausedUntil != nil && inv.State.PausedUntil.Before(inv.State.NextFireAt.UTC()) {
			display.FollowupLabel = "Resumes in"
			display.FollowupValue = formatFutureRelative(inv.State.PausedUntil, now)
		}
		if inv.State.Degraded {
			display.Notes = append(display.Notes, "Degraded after repeated failures; wake attempts stay throttled after resume.")
		}
		if inv.State.SkipNext {
			display.Notes = append(display.Notes, "Skip-next is armed for the shown slot.")
		}
	case inv.State.SkipNext:
		display.HeroLabel = "Next slot"
		display.Modifier = "(skip-next armed, will be skipped)"
		if nextFire := computeFollowingSlot(inv.Schedule, inv.Timezone, inv.State.NextFireAt.UTC()); nextFire != nil {
			display.FollowupLabel = "Next fire"
			display.FollowupValue = formatFutureRelative(nextFire, now)
		}
		if inv.State.Degraded {
			display.Notes = append(display.Notes, "After the skip clears, degraded throttling still applies (~10% fire chance per slot).")
		}
	case inv.State.Degraded:
		display.HeroLabel = "Next slot"
		display.Modifier = "(degraded, ~10% fire chance)"
	}

	return display
}

func buildLastEventDisplay(state schedulepkg.InvocationState, timezone string, now time.Time) lastEventDisplay {
	ts := latestScheduleEventTime(state)
	statusLabel := displayScheduleStatus(state.LastStatus)
	if statusLabel == "" {
		statusLabel = "scheduled"
	}
	display := lastEventDisplay{
		StatusLabel: statusLabel,
		StatusTone:  scheduleOutcomeTone(state.LastStatus),
		Detail:      strings.TrimSpace(state.LastDetail),
	}
	if ts == nil {
		display.Relative = "No recorded event yet"
		display.Tooltip = display.Detail
		return display
	}
	display.HasTimestamp = true
	display.Relative = formatPastRelative(ts, now)
	display.Absolute = formatScheduleTime(ts, timezone)
	tooltipParts := make([]string, 0, 2)
	if display.Absolute != "-" {
		tooltipParts = append(tooltipParts, display.Absolute)
	}
	if display.Detail != "" {
		tooltipParts = append(tooltipParts, display.Detail)
	}
	display.Tooltip = strings.Join(tooltipParts, " · ")
	return display
}

func scheduleInvocationName(inv scheduleInvocationView) string {
	name := strings.TrimSpace(inv.Name)
	if name == "" {
		name = strings.TrimSpace(inv.ID)
	}
	return name
}

func latestScheduleEventTime(state schedulepkg.InvocationState) *time.Time {
	candidates := []*time.Time{
		state.LastFiredAt,
		state.LastSkippedAt,
		state.LastAttemptedAt,
		state.LastEvaluatedAt,
	}
	var latest *time.Time
	for _, candidate := range candidates {
		if candidate == nil {
			continue
		}
		if latest == nil || candidate.After(*latest) {
			copy := candidate.UTC()
			latest = &copy
		}
	}
	return latest
}

func soonestScheduleSlot(invocations []scheduleInvocationView, now time.Time) *time.Time {
	var soonest *time.Time
	for _, inv := range invocations {
		if inv.State.Paused || inv.State.NextFireAt == nil {
			continue
		}
		slot := inv.State.NextFireAt.UTC()
		if slot.Before(now) {
			continue
		}
		if soonest == nil || slot.Before(*soonest) {
			copy := slot
			soonest = &copy
		}
	}
	return soonest
}

func parseDashSchedulePath(path string) (id, action string, ok bool) {
	if !strings.HasPrefix(path, "/schedule/") {
		return "", "", false
	}
	trimmed := strings.TrimSpace(strings.TrimPrefix(path, "/schedule/"))
	if trimmed == "" {
		return "", "", false
	}
	parts := strings.Split(trimmed, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	id, err := url.PathUnescape(parts[0])
	if err != nil || strings.TrimSpace(id) == "" {
		return "", "", false
	}
	return id, parts[1], true
}

func redirectSchedule(w http.ResponseWriter, r *http.Request, notice, errMsg string) {
	target := "/schedule"
	query := url.Values{}
	if strings.TrimSpace(notice) != "" {
		query.Set("notice", notice)
	}
	if strings.TrimSpace(errMsg) != "" {
		query.Set("error", errMsg)
	}
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func formBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func scheduleGateLabel(when *schedulepkg.When) string {
	if when == nil {
		return "cron only"
	}
	session := string(when.SessionOrDefault())
	return strings.TrimSpace(when.Calendar) + " / " + session
}

func scheduleOutcomeTone(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	switch {
	case status == "" || status == "scheduled":
		return "tone-neutral"
	case strings.HasPrefix(status, "fire") || strings.HasPrefix(status, "manual-fire"):
		return "tone-good"
	case strings.Contains(status, "skip") || strings.Contains(status, "pause"):
		return "tone-warning"
	default:
		return "tone-critical"
	}
}

func displayScheduleStatus(status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return "scheduled"
	}
	return strings.ReplaceAll(status, "-", " ")
}

func formatScheduleTime(ts *time.Time, timezone string) string {
	if ts == nil {
		return "-"
	}
	location := loadScheduleLocation(timezone)
	return ts.In(location).Format("Mon 2006-01-02 15:04 MST")
}

func formatLocalInputTime(ts *time.Time, timezone string) string {
	if ts == nil {
		return ""
	}
	location := loadScheduleLocation(timezone)
	return ts.In(location).Format("2006-01-02T15:04")
}

func loadScheduleLocation(timezone string) *time.Location {
	location := time.UTC
	if tz := strings.TrimSpace(timezone); tz != "" {
		if loaded, err := time.LoadLocation(tz); err == nil {
			location = loaded
		}
	}
	return location
}

func formatFutureRelative(ts *time.Time, now time.Time) string {
	if ts == nil {
		return ""
	}
	diff := ts.Sub(now)
	if diff <= 0 {
		return "now"
	}
	return "in " + formatRelativeDuration(diff)
}

func formatPastRelative(ts *time.Time, now time.Time) string {
	if ts == nil {
		return ""
	}
	diff := now.Sub(*ts)
	if diff <= 0 {
		return "just now"
	}
	return formatRelativeDuration(diff) + " ago"
}

func formatRelativeDuration(diff time.Duration) string {
	if diff < time.Minute {
		seconds := int(diff.Round(time.Second) / time.Second)
		if seconds < 1 {
			seconds = 1
		}
		return fmt.Sprintf("%ds", seconds)
	}
	if diff < time.Hour {
		minutes := int(diff.Round(time.Minute) / time.Minute)
		if minutes < 1 {
			minutes = 1
		}
		return fmt.Sprintf("%dm", minutes)
	}
	if diff < 24*time.Hour {
		hours := diff / time.Hour
		minutes := (diff % time.Hour).Round(time.Minute) / time.Minute
		if minutes == 60 {
			hours++
			minutes = 0
		}
		if minutes == 0 {
			return fmt.Sprintf("%dh", hours)
		}
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	days := diff / (24 * time.Hour)
	remainder := diff % (24 * time.Hour)
	hours := remainder.Round(time.Hour) / time.Hour
	if hours == 24 {
		days++
		hours = 0
	}
	if hours == 0 {
		return fmt.Sprintf("%dd", days)
	}
	return fmt.Sprintf("%dd %dh", days, hours)
}

func computeFollowingSlot(scheduleExpr, timezone string, after time.Time) *time.Time {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	compiled, err := parser.Parse(strings.TrimSpace(scheduleExpr))
	if err != nil {
		return nil
	}
	location := loadScheduleLocation(timezone)
	next := compiled.Next(after.In(location)).UTC()
	return &next
}

func convertLocalPauseUntil(raw, timezone string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	location := loadScheduleLocation(timezone)
	parsed, err := time.ParseInLocation("2006-01-02T15:04", raw, location)
	if err != nil {
		return "", fmt.Errorf("until must be a valid local date/time")
	}
	return parsed.UTC().Format(time.RFC3339), nil
}

func toneForCount(count int) string {
	if count <= 0 {
		return "tone-good"
	}
	return "tone-warning"
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
