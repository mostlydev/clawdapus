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
)

type schedulePageData struct {
	PodName         string
	ActiveTab       string
	HasSchedule     bool
	Summary         []dashStat
	Rows            []scheduleRow
	HasRows         bool
	Notice          string
	Error           string
	HasNotice       bool
	HasError        bool
	HasStatusErrors bool
}

type scheduleRow struct {
	ID              string
	Service         string
	AgentID         string
	Name            string
	Message         string
	Target          string
	Gate            string
	Schedule        string
	Timezone        string
	NextFire        string
	LastStatus      string
	LastStatusTone  string
	LastDetail      string
	WakeAdapter     string
	WakeTarget      string
	WakeCommand     string
	Badges          []scheduleBadge
	PauseSummary    string
	LastEvent       string
	BypassFireLabel string
}

type scheduleBadge struct {
	Label string
	Tone  string
}

func (h *handler) renderSchedule(w http.ResponseWriter, r *http.Request) {
	if !h.hasSchedule() {
		http.NotFound(w, r)
		return
	}

	invocations, err := h.scheduleSource.List(r.Context())
	data := buildSchedulePageData(
		h.manifest.PodName,
		invocations,
		strings.TrimSpace(r.URL.Query().Get("notice")),
		firstNonEmpty(strings.TrimSpace(r.URL.Query().Get("error")), errString(err)),
	)

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
		err = h.scheduleSource.Pause(ctx, id, r.FormValue("until"), r.FormValue("reason"))
		notice = fmt.Sprintf("Paused %s.", id)
	case "resume":
		err = h.scheduleSource.Resume(ctx, id)
		notice = fmt.Sprintf("Resumed %s.", id)
	case "skip-next":
		err = h.scheduleSource.SkipNext(ctx, id)
		notice = fmt.Sprintf("Marked %s to skip the next fire.", id)
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

func buildSchedulePageData(podName string, invocations []scheduleInvocationView, notice, errMsg string) schedulePageData {
	rows := make([]scheduleRow, 0, len(invocations))
	serviceSet := make(map[string]struct{})
	paused := 0
	skipNext := 0
	degraded := 0
	gated := 0

	sort.Slice(invocations, func(i, j int) bool {
		if invocations[i].Service != invocations[j].Service {
			return invocations[i].Service < invocations[j].Service
		}
		if invocations[i].Name != invocations[j].Name {
			return invocations[i].Name < invocations[j].Name
		}
		return invocations[i].ID < invocations[j].ID
	})

	for _, inv := range invocations {
		serviceSet[inv.Service] = struct{}{}
		if inv.When != nil {
			gated++
		}
		if inv.State.Paused {
			paused++
		}
		if inv.State.SkipNext {
			skipNext++
		}
		if inv.State.Degraded {
			degraded++
		}
		rows = append(rows, buildScheduleRow(inv))
	}

	summary := []dashStat{
		{Label: "Invocations", Value: fmt.Sprintf("%d", len(invocations)), Hint: "scheduler-owned jobs in scope", Tone: "neutral"},
		{Label: "Services", Value: fmt.Sprintf("%d", len(serviceSet)), Hint: "services with scheduled work", Tone: "neutral"},
		{Label: "Gated", Value: fmt.Sprintf("%d", gated), Hint: "calendar/session constrained", Tone: toneForCount(gated)},
		{Label: "Paused", Value: fmt.Sprintf("%d", paused), Hint: "manually paused right now", Tone: toneForCount(paused)},
		{Label: "Skip next", Value: fmt.Sprintf("%d", skipNext), Hint: "one-shot skip pending", Tone: toneForCount(skipNext)},
		{Label: "Degraded", Value: fmt.Sprintf("%d", degraded), Hint: "throttled after repeated failures", Tone: toneForCount(degraded)},
	}

	return schedulePageData{
		PodName:     podName,
		ActiveTab:   "schedule",
		HasSchedule: true,
		Summary:     summary,
		Rows:        rows,
		HasRows:     len(rows) > 0,
		Notice:      notice,
		Error:       errMsg,
		HasNotice:   strings.TrimSpace(notice) != "",
		HasError:    strings.TrimSpace(errMsg) != "",
	}
}

func buildScheduleRow(inv scheduleInvocationView) scheduleRow {
	name := strings.TrimSpace(inv.Name)
	if name == "" {
		name = inv.ID
	}

	badges := make([]scheduleBadge, 0, 3)
	pauseSummary := ""
	if inv.State.Paused {
		label := "paused"
		if inv.State.PausedUntil != nil {
			label = "paused until " + formatScheduleTime(inv.State.PausedUntil, inv.Timezone)
		}
		badges = append(badges, scheduleBadge{Label: label, Tone: "tone-warning"})
		if strings.TrimSpace(inv.State.PauseReason) != "" {
			pauseSummary = inv.State.PauseReason
		}
	}
	if inv.State.SkipNext {
		badges = append(badges, scheduleBadge{Label: "skip next", Tone: "tone-neutral"})
	}
	if inv.State.Degraded {
		badges = append(badges, scheduleBadge{Label: "degraded", Tone: "tone-critical"})
	}

	lastStatus := strings.TrimSpace(inv.State.LastStatus)
	if lastStatus == "" {
		lastStatus = "scheduled"
	}

	return scheduleRow{
		ID:              inv.ID,
		Service:         inv.Service,
		AgentID:         inv.AgentID,
		Name:            name,
		Message:         strings.TrimSpace(inv.Message),
		Target:          strings.TrimSpace(inv.To),
		Gate:            scheduleGateLabel(inv.When),
		Schedule:        strings.TrimSpace(inv.Schedule),
		Timezone:        firstNonEmpty(strings.TrimSpace(inv.Timezone), "UTC"),
		NextFire:        formatScheduleTime(inv.State.NextFireAt, inv.Timezone),
		LastStatus:      lastStatus,
		LastStatusTone:  scheduleOutcomeTone(lastStatus),
		LastDetail:      strings.TrimSpace(inv.State.LastDetail),
		WakeAdapter:     strings.TrimSpace(inv.Wake.Adapter),
		WakeTarget:      strings.TrimSpace(inv.Wake.Target),
		WakeCommand:     strings.Join(inv.Wake.Command, " "),
		Badges:          badges,
		PauseSummary:    pauseSummary,
		LastEvent:       formatLastScheduleEvent(inv.State, inv.Timezone),
		BypassFireLabel: "Force fire",
	}
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

func formatScheduleTime(ts *time.Time, timezone string) string {
	if ts == nil {
		return "-"
	}
	location := time.UTC
	if tz := strings.TrimSpace(timezone); tz != "" {
		if loaded, err := time.LoadLocation(tz); err == nil {
			location = loaded
		}
	}
	return ts.In(location).Format("2006-01-02 15:04 MST")
}

func formatLastScheduleEvent(state schedulepkg.InvocationState, timezone string) string {
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
			latest = candidate
		}
	}
	return formatScheduleTime(latest, timezone)
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
