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
	MCP         *MCPDescriptor       `json:"mcp,omitempty"`
	Tools       []ToolDescriptor     `json:"tools,omitempty"`
	Memory      *MemoryDescriptor    `json:"memory,omitempty"`
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

type ToolDescriptor struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
	HTTP        *ToolHTTP              `json:"http,omitempty"`
	Annotations map[string]interface{} `json:"annotations,omitempty"`
}

type MCPDescriptor struct {
	Transport string `json:"transport,omitempty"`
	Path      string `json:"path,omitempty"`
}

type ToolHTTP struct {
	Method  string `json:"method"`
	Path    string `json:"path"`
	Body    string `json:"body,omitempty"`
	BodyKey string `json:"body_key,omitempty"`
}

type MemoryDescriptor struct {
	Recall *MemoryEndpoint `json:"recall,omitempty"`
	Retain *MemoryEndpoint `json:"retain,omitempty"`
	Forget *MemoryEndpoint `json:"forget,omitempty"`
}

type MemoryEndpoint struct {
	Path string `json:"path"`
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
	switch d.Version {
	case 1, 2:
	default:
		return fmt.Errorf("descriptor version must be 1 or 2, got %d", d.Version)
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

	if d.Version == 1 {
		if len(d.Tools) > 0 {
			return fmt.Errorf("descriptor version 1 does not support tools")
		}
		if d.MCP != nil {
			return fmt.Errorf("descriptor version 1 does not support mcp")
		}
		if d.Memory != nil {
			return fmt.Errorf("descriptor version 1 does not support memory")
		}
	}

	if d.Version >= 2 {
		if err := validateMCP(d.MCP); err != nil {
			return err
		}
		if err := validateTools(d.Tools, d.MCP); err != nil {
			return err
		}
		if err := validateMemory(d.Memory); err != nil {
			return err
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

func validateMCP(mcp *MCPDescriptor) error {
	if mcp == nil {
		return nil
	}
	transport := strings.ToLower(strings.TrimSpace(mcp.Transport))
	switch transport {
	case "", "http", "streamable_http", "streamable-http":
		mcp.Transport = "streamable_http"
	default:
		return fmt.Errorf("mcp.transport %q is unsupported", mcp.Transport)
	}
	mcp.Path = strings.TrimSpace(mcp.Path)
	if mcp.Path == "" {
		mcp.Path = "/mcp"
	}
	if !strings.HasPrefix(mcp.Path, "/") {
		return fmt.Errorf("mcp.path %q must start with '/'", mcp.Path)
	}
	return nil
}

func validateTools(tools []ToolDescriptor, mcp *MCPDescriptor) error {
	seenTools := make(map[string]struct{}, len(tools))
	for i := range tools {
		tool := &tools[i]
		tool.Name = strings.TrimSpace(tool.Name)
		tool.Description = strings.TrimSpace(tool.Description)
		if tool.Name == "" {
			return fmt.Errorf("tools[%d]: name is required", i)
		}
		if _, exists := seenTools[tool.Name]; exists {
			return fmt.Errorf("tools[%d]: duplicate tool name %q", i, tool.Name)
		}
		seenTools[tool.Name] = struct{}{}
		if tool.Description == "" {
			return fmt.Errorf("tools[%d]: description is required", i)
		}
		if tool.InputSchema == nil {
			return fmt.Errorf("tools[%d]: inputSchema is required", i)
		}
		schemaType, ok := tool.InputSchema["type"].(string)
		if !ok || strings.TrimSpace(schemaType) == "" {
			return fmt.Errorf("tools[%d]: inputSchema.type must be \"object\"", i)
		}
		if strings.ToLower(strings.TrimSpace(schemaType)) != "object" {
			return fmt.Errorf("tools[%d]: inputSchema.type must be \"object\"", i)
		}
		if mcp != nil && tool.HTTP != nil {
			return fmt.Errorf("tools[%d]: http must not be set when descriptor mcp is set", i)
		}
		if tool.HTTP == nil {
			if mcp != nil {
				continue
			}
			return fmt.Errorf("tools[%d]: http is required", i)
		}
		tool.HTTP.Method = strings.ToUpper(strings.TrimSpace(tool.HTTP.Method))
		tool.HTTP.Path = strings.TrimSpace(tool.HTTP.Path)
		tool.HTTP.Body = strings.ToLower(strings.TrimSpace(tool.HTTP.Body))
		tool.HTTP.BodyKey = strings.TrimSpace(tool.HTTP.BodyKey)
		switch tool.HTTP.Method {
		case "GET", "POST", "PUT", "PATCH", "DELETE":
		default:
			return fmt.Errorf("tools[%d]: http.method %q is unsupported", i, tool.HTTP.Method)
		}
		if tool.HTTP.Path == "" {
			return fmt.Errorf("tools[%d]: http.path is required", i)
		}
		if !strings.HasPrefix(tool.HTTP.Path, "/") {
			return fmt.Errorf("tools[%d]: http.path %q must start with '/'", i, tool.HTTP.Path)
		}
		switch tool.HTTP.Body {
		case "", "json":
		default:
			return fmt.Errorf("tools[%d]: http.body %q is unsupported", i, tool.HTTP.Body)
		}
		if tool.HTTP.BodyKey != "" {
			switch tool.HTTP.Method {
			case "POST", "PUT", "PATCH":
			default:
				return fmt.Errorf("tools[%d]: http.body_key requires POST, PUT, or PATCH", i)
			}
		}
	}
	return nil
}

func validateMemory(memory *MemoryDescriptor) error {
	if memory == nil {
		return nil
	}
	if memory.Recall == nil && memory.Retain == nil {
		return fmt.Errorf("memory: at least one of recall or retain must be declared")
	}
	for name, endpoint := range map[string]*MemoryEndpoint{
		"recall": memory.Recall,
		"retain": memory.Retain,
		"forget": memory.Forget,
	} {
		if endpoint == nil {
			continue
		}
		endpoint.Path = strings.TrimSpace(endpoint.Path)
		if endpoint.Path == "" {
			return fmt.Errorf("memory.%s.path is required", name)
		}
		if !strings.HasPrefix(endpoint.Path, "/") {
			return fmt.Errorf("memory.%s.path %q must start with '/'", name, endpoint.Path)
		}
	}
	return nil
}
