package mcpdiscover

import (
	"fmt"
	"strings"

	"github.com/mostlydev/clawdapus/internal/describe"
)

func Descriptor(description, path string, result *Result, metadata *describe.DiscoveryMetadata) (*describe.ServiceDescriptor, error) {
	if result == nil {
		return nil, fmt.Errorf("mcp discovery result is required")
	}
	tools := make([]describe.ToolDescriptor, 0, len(result.Tools))
	for _, tool := range result.Tools {
		name := strings.TrimSpace(tool.Name)
		toolDescription := strings.TrimSpace(tool.Description)
		if toolDescription == "" && name != "" {
			toolDescription = fmt.Sprintf("MCP tool %s.", name)
		}
		schema := normalizeInputSchema(tool.InputSchema)
		tools = append(tools, describe.ToolDescriptor{
			Name:        name,
			Description: toolDescription,
			InputSchema: schema,
			Annotations: tool.Annotations,
		})
	}

	descriptor := &describe.ServiceDescriptor{
		Version:        2,
		Description:    strings.TrimSpace(description),
		MCP:            &describe.MCPDescriptor{Transport: "streamable_http", Path: path},
		Tools:          tools,
		XClawDiscovery: metadata,
	}
	if descriptor.Description == "" {
		descriptor.Description = "Discovered MCP stdio tools."
	}
	if metadata != nil {
		metadata.MCPProtocolVersion = strings.TrimSpace(result.ProtocolVersion)
		metadata.ToolCount = len(tools)
	}
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}
	return descriptor, nil
}

func normalizeInputSchema(schema map[string]interface{}) map[string]interface{} {
	if schema == nil {
		return map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		}
	}
	out := make(map[string]interface{}, len(schema)+1)
	for key, value := range schema {
		out[key] = value
	}
	if _, ok := out["type"]; !ok {
		out["type"] = "object"
	}
	return out
}
