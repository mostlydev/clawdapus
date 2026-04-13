package main

import (
	"net/http"
	"testing"
	"time"
)

func TestParseDiscordRateLimitHeaderOnly(t *testing.T) {
	now := time.Unix(100, 0)
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"2.5"}},
	}

	limit := parseDiscordRateLimit(resp, nil, now)
	if limit == nil {
		t.Fatal("expected rate limit")
	}
	if limit.Scope != rateLimitScopeChannel {
		t.Fatalf("expected channel scope, got %q", limit.Scope)
	}
	if limit.RetryAfter != 2500*time.Millisecond {
		t.Fatalf("expected 2.5s retry, got %s", limit.RetryAfter)
	}
}

func TestParseDiscordRateLimitBodyOnly(t *testing.T) {
	now := time.Unix(100, 0)
	resp := &http.Response{StatusCode: http.StatusTooManyRequests, Header: make(http.Header)}

	limit := parseDiscordRateLimit(resp, []byte(`{"retry_after":1.25}`), now)
	if limit == nil {
		t.Fatal("expected rate limit")
	}
	if limit.RetryAfter != 1250*time.Millisecond {
		t.Fatalf("expected 1.25s retry, got %s", limit.RetryAfter)
	}
}

func TestParseDiscordRateLimitPrefersLongestRetryHint(t *testing.T) {
	now := time.Unix(100, 0)
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     make(http.Header),
	}
	resp.Header.Set("Retry-After", "1")
	resp.Header.Set("X-RateLimit-Reset-After", "2.75")

	limit := parseDiscordRateLimit(resp, []byte(`{"retry_after":2.5}`), now)
	if limit == nil {
		t.Fatal("expected rate limit")
	}
	if limit.RetryAfter != 2750*time.Millisecond {
		t.Fatalf("expected longest retry hint, got %s", limit.RetryAfter)
	}
}

func TestParseDiscordRateLimitUsesGlobalScopeFromBody(t *testing.T) {
	now := time.Unix(100, 0)
	resp := &http.Response{StatusCode: http.StatusTooManyRequests, Header: make(http.Header)}

	limit := parseDiscordRateLimit(resp, []byte(`{"retry_after":1,"global":true}`), now)
	if limit == nil {
		t.Fatal("expected rate limit")
	}
	if limit.Scope != rateLimitScopeToken {
		t.Fatalf("expected token scope, got %q", limit.Scope)
	}
}

func TestParseDiscordRateLimitUsesGlobalScopeFromHeader(t *testing.T) {
	now := time.Unix(100, 0)
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     make(http.Header),
	}
	resp.Header.Set("Retry-After", "3")
	resp.Header.Set("X-RateLimit-Scope", "global")

	limit := parseDiscordRateLimit(resp, nil, now)
	if limit == nil {
		t.Fatal("expected rate limit")
	}
	if limit.Scope != rateLimitScopeToken {
		t.Fatalf("expected token scope, got %q", limit.Scope)
	}
}

func TestParseDiscordRateLimitFallsBackWhenTimingMissing(t *testing.T) {
	now := time.Unix(100, 0)
	resp := &http.Response{StatusCode: http.StatusTooManyRequests, Header: make(http.Header)}

	limit := parseDiscordRateLimit(resp, []byte(`{"message":"rate limited"}`), now)
	if limit == nil {
		t.Fatal("expected rate limit")
	}
	if limit.RetryAfter != discordRateLimitFallback {
		t.Fatalf("expected fallback retry, got %s", limit.RetryAfter)
	}
}

func TestParseDiscordRateLimitIgnoresNon429(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusForbidden, Header: make(http.Header)}
	if limit := parseDiscordRateLimit(resp, []byte(`{"retry_after":1}`), time.Unix(100, 0)); limit != nil {
		t.Fatalf("expected nil rate limit, got %+v", limit)
	}
}

func TestRateLimitTrackerBlocksOnlyMatchingChannel(t *testing.T) {
	tracker := newRateLimitTracker()
	now := time.Unix(100, 0)
	tracker.record(tokenPair{ChannelID: "chan-1", Token: "token-a"}, rateLimit{
		Scope:      rateLimitScopeChannel,
		RetryAfter: 5 * time.Second,
		RecordedAt: now,
	})

	if !tracker.blocked(tokenPair{ChannelID: "chan-1", Token: "token-a"}, now.Add(time.Second)) {
		t.Fatal("expected matching channel to be blocked")
	}
	if tracker.blocked(tokenPair{ChannelID: "chan-2", Token: "token-a"}, now.Add(time.Second)) {
		t.Fatal("did not expect other channel on same token to be blocked")
	}
	if tracker.blocked(tokenPair{ChannelID: "chan-1", Token: "token-b"}, now.Add(time.Second)) {
		t.Fatal("did not expect same channel on other token to be blocked")
	}
	if tracker.blocked(tokenPair{ChannelID: "chan-1", Token: "token-a"}, now.Add(6*time.Second)) {
		t.Fatal("expected block to expire")
	}
}

func TestRateLimitTrackerBlocksAllChannelsForToken(t *testing.T) {
	tracker := newRateLimitTracker()
	now := time.Unix(100, 0)
	tracker.record(tokenPair{ChannelID: "chan-1", Token: "token-a"}, rateLimit{
		Scope:      rateLimitScopeToken,
		RetryAfter: 5 * time.Second,
		RecordedAt: now,
	})

	if !tracker.blocked(tokenPair{ChannelID: "chan-1", Token: "token-a"}, now.Add(time.Second)) {
		t.Fatal("expected matching token/channel to be blocked")
	}
	if !tracker.blocked(tokenPair{ChannelID: "chan-2", Token: "token-a"}, now.Add(time.Second)) {
		t.Fatal("expected other channel on same token to be blocked")
	}
	if tracker.blocked(tokenPair{ChannelID: "chan-2", Token: "token-b"}, now.Add(time.Second)) {
		t.Fatal("did not expect other token to be blocked")
	}
}
