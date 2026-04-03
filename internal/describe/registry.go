package describe

import "fmt"

type FeedSpec struct {
	Name        string
	Source      string
	Path        string
	TTL         int
	Description string
	Auth        *AuthDescriptor
}

type ToolSpec struct {
	Name        string
	Service     string
	Description string
	InputSchema map[string]interface{}
	HTTP        *ToolHTTP
	Annotations map[string]interface{}
}

type ToolRegistry map[string][]ToolSpec

func BuildFeedRegistry(descriptors map[string]*ServiceDescriptor) (map[string]FeedSpec, error) {
	registry := make(map[string]FeedSpec)
	for serviceName, descriptor := range descriptors {
		if descriptor == nil {
			continue
		}
		for _, feed := range descriptor.Feeds {
			if existing, exists := registry[feed.Name]; exists {
				return nil, fmt.Errorf("feed name %q is declared by both %q and %q", feed.Name, existing.Source, serviceName)
			}
			spec := FeedSpec{
				Name:        feed.Name,
				Source:      serviceName,
				Path:        feed.Path,
				TTL:         feed.TTL,
				Description: feed.Description,
			}
			if descriptor.Auth != nil {
				auth := *descriptor.Auth
				spec.Auth = &auth
			}
			registry[feed.Name] = spec
		}
	}
	return registry, nil
}

func BuildToolRegistry(descriptors map[string]*ServiceDescriptor) (ToolRegistry, error) {
	registry := make(ToolRegistry)
	for serviceName, descriptor := range descriptors {
		if descriptor == nil || len(descriptor.Tools) == 0 {
			continue
		}
		seen := make(map[string]struct{}, len(descriptor.Tools))
		specs := make([]ToolSpec, 0, len(descriptor.Tools))
		for _, tool := range descriptor.Tools {
			if _, exists := seen[tool.Name]; exists {
				return nil, fmt.Errorf("tool name %q is declared multiple times by %q", tool.Name, serviceName)
			}
			seen[tool.Name] = struct{}{}
			spec := ToolSpec{
				Name:        tool.Name,
				Service:     serviceName,
				Description: tool.Description,
				InputSchema: tool.InputSchema,
				HTTP:        tool.HTTP,
				Annotations: tool.Annotations,
			}
			specs = append(specs, spec)
		}
		registry[serviceName] = specs
	}
	return registry, nil
}
