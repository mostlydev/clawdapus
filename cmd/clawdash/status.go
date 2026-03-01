package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

type serviceStatus struct {
	Service        string `json:"service"`
	Status         string `json:"status"`
	State          string `json:"state"`
	Health         string `json:"health,omitempty"`
	Uptime         string `json:"uptime"`
	ContainerID    string `json:"containerId,omitempty"`
	Instances      int    `json:"instances"`
	Running        int    `json:"running"`
	HasCllamaToken bool   `json:"hasCllamaToken,omitempty"`
}

type dockerStatusSource struct {
	podName string
	cli     *client.Client
	now     func() time.Time
}

type instance struct {
	id             string
	status         string
	state          string
	health         string
	startedAt      time.Time
	running        bool
	hasCllamaToken bool
}

func newDockerStatusSource(podName string) (*dockerStatusSource, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &dockerStatusSource{
		podName: podName,
		cli:     cli,
		now:     time.Now,
	}, nil
}

func (d *dockerStatusSource) Close() error {
	return d.cli.Close()
}

func (d *dockerStatusSource) Ping(ctx context.Context) error {
	_, err := d.cli.Ping(ctx)
	return err
}

func (d *dockerStatusSource) Snapshot(ctx context.Context, serviceNames []string) (map[string]serviceStatus, error) {
	nameSet := make(map[string]struct{}, len(serviceNames))
	out := make(map[string]serviceStatus, len(serviceNames))
	for _, name := range serviceNames {
		nameSet[name] = struct{}{}
		out[name] = unknownStatus(name)
	}

	args := filters.NewArgs(filters.Arg("label", "claw.pod="+d.podName))
	containers, err := d.cli.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: args,
	})
	if err != nil {
		return nil, err
	}

	buckets := make(map[string][]instance)
	for _, c := range containers {
		serviceName := serviceNameFromLabels(c.Labels, c.Names)
		if serviceName == "" {
			continue
		}
		if _, ok := nameSet[serviceName]; !ok {
			continue
		}

		inspect, err := d.cli.ContainerInspect(ctx, c.ID)
		if err != nil {
			continue
		}
		inst := containerToInstance(inspect)
		buckets[serviceName] = append(buckets[serviceName], inst)
	}

	now := d.now()
	for serviceName, instances := range buckets {
		out[serviceName] = aggregateInstances(serviceName, instances, now)
	}

	return out, nil
}

func unknownStatus(service string) serviceStatus {
	return serviceStatus{
		Service:   service,
		Status:    "unknown",
		State:     "unknown",
		Uptime:    "-",
		Instances: 0,
		Running:   0,
	}
}

func serviceNameFromLabels(labels map[string]string, names []string) string {
	if labels == nil {
		labels = map[string]string{}
	}
	if v := strings.TrimSpace(labels["claw.service"]); v != "" {
		return v
	}
	if v := strings.TrimSpace(labels["com.docker.compose.service"]); v != "" {
		return v
	}
	if len(names) > 0 {
		return strings.TrimPrefix(names[0], "/")
	}
	return ""
}

func containerToInstance(info types.ContainerJSON) instance {
	state := "unknown"
	health := ""
	running := false
	startedAt := time.Time{}

	if info.ContainerJSONBase != nil && info.State != nil {
		state = strings.ToLower(strings.TrimSpace(info.State.Status))
		running = info.State.Running
		if info.State.Health != nil {
			health = strings.ToLower(strings.TrimSpace(info.State.Health.Status))
		}
		if started := strings.TrimSpace(info.State.StartedAt); started != "" {
			if ts, err := time.Parse(time.RFC3339Nano, started); err == nil {
				startedAt = ts
			}
		}
	}

	hasToken := false
	if info.Config != nil {
		for _, raw := range info.Config.Env {
			k, v, ok := strings.Cut(raw, "=")
			if !ok {
				continue
			}
			if strings.TrimSpace(k) == "CLLAMA_TOKEN" && strings.TrimSpace(v) != "" {
				hasToken = true
				break
			}
		}
	}

	return instance{
		id:             info.ID,
		status:         normalizeStatus(state, running, health),
		state:          state,
		health:         health,
		startedAt:      startedAt,
		running:        running,
		hasCllamaToken: hasToken,
	}
}

func aggregateInstances(service string, instances []instance, now time.Time) serviceStatus {
	if len(instances) == 0 {
		return unknownStatus(service)
	}

	sort.Slice(instances, func(i, j int) bool {
		return statusSeverity(instances[i].status) > statusSeverity(instances[j].status)
	})
	worst := instances[0]

	running := 0
	hasToken := false
	longest := time.Duration(0)
	for _, inst := range instances {
		if inst.running {
			running++
			if !inst.startedAt.IsZero() {
				if dur := now.Sub(inst.startedAt); dur > longest {
					longest = dur
				}
			}
		}
		if inst.hasCllamaToken {
			hasToken = true
		}
	}

	uptime := "-"
	if longest > 0 {
		uptime = formatDuration(longest)
	}

	return serviceStatus{
		Service:        service,
		Status:         worst.status,
		State:          worst.state,
		Health:         worst.health,
		Uptime:         uptime,
		ContainerID:    shortID(worst.id),
		Instances:      len(instances),
		Running:        running,
		HasCllamaToken: hasToken,
	}
}

func normalizeStatus(state string, running bool, health string) string {
	if running {
		if health == "healthy" || health == "unhealthy" || health == "starting" {
			return health
		}
		return "running"
	}

	switch state {
	case "restarting", "created", "paused":
		return "starting"
	case "dead", "exited", "removing", "":
		return "stopped"
	default:
		return state
	}
}

func statusSeverity(status string) int {
	switch status {
	case "healthy":
		return 0
	case "running":
		return 1
	case "starting":
		return 2
	case "unknown":
		return 2
	case "unhealthy":
		return 3
	case "stopped":
		return 4
	default:
		return 3
	}
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	h := int(d / time.Hour)
	d -= time.Duration(h) * time.Hour
	m := int(d / time.Minute)
	d -= time.Duration(m) * time.Minute
	s := int(d / time.Second)

	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func shortID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}
