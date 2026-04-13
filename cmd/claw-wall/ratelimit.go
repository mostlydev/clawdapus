package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const discordRateLimitFallback = 5 * time.Second

type rateLimitScope string

const (
	rateLimitScopeChannel rateLimitScope = "channel"
	rateLimitScopeToken   rateLimitScope = "token"
)

type rateLimit struct {
	Scope      rateLimitScope
	RetryAfter time.Duration
	RecordedAt time.Time
}

type discordRateLimitError struct {
	Target          tokenPair
	Limit           rateLimit
	FirstOccurrence bool
}

func (e *discordRateLimitError) Error() string {
	return fmt.Sprintf(
		"discord rate limited %s %q; retry after %s",
		describeRateLimitTarget(e.Limit.Scope),
		e.Target.ChannelID,
		e.Limit.RetryAfter.Round(time.Millisecond),
	)
}

type rateLimitTracker struct {
	channelExpiry map[string]time.Time
	tokenExpiry   map[string]time.Time
}

type discordRateLimitPayload struct {
	RetryAfter float64 `json:"retry_after"`
	Global     bool    `json:"global"`
}

func newRateLimitTracker() *rateLimitTracker {
	return &rateLimitTracker{
		channelExpiry: make(map[string]time.Time),
		tokenExpiry:   make(map[string]time.Time),
	}
}

func (t *rateLimitTracker) blocked(target tokenPair, now time.Time) bool {
	if t == nil {
		return false
	}
	if expiry, ok := t.tokenExpiry[target.Token]; ok {
		if expiry.After(now) {
			return true
		}
		delete(t.tokenExpiry, target.Token)
	}
	if expiry, ok := t.channelExpiry[pairKey(target)]; ok {
		if expiry.After(now) {
			return true
		}
		delete(t.channelExpiry, pairKey(target))
	}
	return false
}

func (t *rateLimitTracker) record(target tokenPair, limit rateLimit) bool {
	if t == nil {
		return false
	}

	expiry := limit.RecordedAt.Add(limit.RetryAfter)
	switch limit.Scope {
	case rateLimitScopeToken:
		if !expiry.After(t.tokenExpiry[target.Token]) {
			return false
		}
		t.tokenExpiry[target.Token] = expiry
		return true
	default:
		key := pairKey(target)
		if !expiry.After(t.channelExpiry[key]) {
			return false
		}
		t.channelExpiry[key] = expiry
		return true
	}
}

func parseDiscordRateLimit(resp *http.Response, body []byte, now time.Time) *rateLimit {
	if resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		return nil
	}

	scope := rateLimitScopeChannel
	if strings.EqualFold(strings.TrimSpace(resp.Header.Get("X-RateLimit-Scope")), "global") {
		scope = rateLimitScopeToken
	}

	durations := make([]time.Duration, 0, 3)
	if duration, ok := parseRetryAfterHeader(resp.Header.Get("Retry-After"), now); ok {
		durations = append(durations, duration)
	}
	if duration, ok := parseSecondsDuration(resp.Header.Get("X-RateLimit-Reset-After")); ok {
		durations = append(durations, duration)
	}

	var payload discordRateLimitPayload
	if len(body) > 0 && json.Unmarshal(body, &payload) == nil {
		if duration, ok := secondsDuration(payload.RetryAfter); ok {
			durations = append(durations, duration)
		}
		if payload.Global {
			scope = rateLimitScopeToken
		}
	}

	retryAfter := maxDuration(durations...)
	if retryAfter <= 0 {
		retryAfter = discordRateLimitFallback
	}

	return &rateLimit{
		Scope:      scope,
		RetryAfter: retryAfter,
		RecordedAt: now,
	}
}

func parseRetryAfterHeader(raw string, now time.Time) (time.Duration, bool) {
	if duration, ok := parseSecondsDuration(raw); ok {
		return duration, true
	}

	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, false
	}
	at, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	duration := at.Sub(now)
	if duration <= 0 {
		return 0, false
	}
	return duration, true
}

func parseSecondsDuration(raw string) (time.Duration, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, false
	}
	seconds, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, false
	}
	return secondsDuration(seconds)
}

func secondsDuration(seconds float64) (time.Duration, bool) {
	if seconds <= 0 {
		return 0, false
	}
	return time.Duration(seconds * float64(time.Second)), true
}

func maxDuration(values ...time.Duration) time.Duration {
	var longest time.Duration
	for _, value := range values {
		if value > longest {
			longest = value
		}
	}
	return longest
}

func describeRateLimitTarget(scope rateLimitScope) string {
	switch scope {
	case rateLimitScopeToken:
		return "token"
	default:
		return "channel"
	}
}
