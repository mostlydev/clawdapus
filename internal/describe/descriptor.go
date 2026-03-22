package describe

import (
	"encoding/json"
	"fmt"
	"strings"
)

const DefaultDescriptorFile = ".claw-describe.json"

type ServiceDescriptor struct {
	Version     int                  `json:"version"`
	Description string               `json:"description,omitempty"`
	Feeds       []FeedDescriptor     `json:"feeds,omitempty"`
	Endpoints   []EndpointDescriptor `json:"endpoints,omitempty"`
	Auth        *AuthDescriptor      `json:"auth,omitempty"`
	Skill       string               `json:"skill,omitempty"`
}

type FeedDescriptor struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	TTL         int    `json:"ttl"`
	Description string `json:"description,omitempty"`
}

type EndpointDescriptor struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
}

type AuthDescriptor struct {
	Type string `json:"type"`
	Env  string `json:"env,omitempty"`
}

func Parse(data []byte) (*ServiceDescriptor, error) {
	var descriptor ServiceDescriptor
	if err := json.Unmarshal(data, &descriptor); err != nil {
		return nil, fmt.Errorf("parse descriptor JSON: %w", err)
	}
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}
	return &descriptor, nil
}

func (d *ServiceDescriptor) Validate() error {
	if d == nil {
		return nil
	}
	if d.Version != 1 {
		return fmt.Errorf("descriptor version must be 1, got %d", d.Version)
	}

	seenFeeds := make(map[string]struct{}, len(d.Feeds))
	for i := range d.Feeds {
		feed := &d.Feeds[i]
		feed.Name = strings.TrimSpace(feed.Name)
		feed.Path = strings.TrimSpace(feed.Path)
		feed.Description = strings.TrimSpace(feed.Description)
		if feed.Name == "" {
			return fmt.Errorf("feeds[%d]: name is required", i)
		}
		if _, exists := seenFeeds[feed.Name]; exists {
			return fmt.Errorf("feeds[%d]: duplicate feed name %q", i, feed.Name)
		}
		seenFeeds[feed.Name] = struct{}{}
		if feed.Path == "" {
			return fmt.Errorf("feeds[%d]: path is required", i)
		}
		if !strings.HasPrefix(feed.Path, "/") {
			return fmt.Errorf("feeds[%d]: path %q must start with '/'", i, feed.Path)
		}
		if feed.TTL <= 0 {
			return fmt.Errorf("feeds[%d]: ttl must be > 0", i)
		}
	}

	for i := range d.Endpoints {
		endpoint := &d.Endpoints[i]
		endpoint.Method = strings.ToUpper(strings.TrimSpace(endpoint.Method))
		endpoint.Path = strings.TrimSpace(endpoint.Path)
		endpoint.Description = strings.TrimSpace(endpoint.Description)
		if endpoint.Method == "" {
			return fmt.Errorf("endpoints[%d]: method is required", i)
		}
		if endpoint.Path == "" {
			return fmt.Errorf("endpoints[%d]: path is required", i)
		}
		if !strings.HasPrefix(endpoint.Path, "/") {
			return fmt.Errorf("endpoints[%d]: path %q must start with '/'", i, endpoint.Path)
		}
	}

	if d.Auth != nil {
		d.Auth.Type = strings.ToLower(strings.TrimSpace(d.Auth.Type))
		d.Auth.Env = strings.TrimSpace(d.Auth.Env)
		switch d.Auth.Type {
		case "bearer", "header", "none":
		default:
			return fmt.Errorf("auth.type %q is unsupported", d.Auth.Type)
		}
	}

	d.Description = strings.TrimSpace(d.Description)
	d.Skill = strings.TrimSpace(d.Skill)
	return nil
}
