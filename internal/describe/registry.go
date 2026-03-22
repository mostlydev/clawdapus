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
