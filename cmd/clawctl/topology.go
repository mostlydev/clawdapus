package main

import (
	"fmt"
	"sort"
	"strings"

	manifestpkg "github.com/mostlydev/clawdapus/internal/clawctl"
)

type topologyPageData struct {
	PodName         string
	ActiveTab       string
	CanvasWidth     int
	CanvasHeight    int
	Nodes           []topologyNode
	Edges           []topologyEdge
	StatusError     string
	HasStatusErrors bool
}

type topologyNode struct {
	ID          string
	Label       string
	Lane        string
	ServiceName string
	X           int
	Y           int
	Width       int
	Height      int
	Status      string
	StatusClass string
	Neighbors   string
}

type topologyEdge struct {
	FromID string
	ToID   string
	Path   string
	Color  string
}

type edgeDef struct {
	fromLane string
	fromName string
	toLane   string
	toName   string
	kind     string
}

func buildTopologyPageData(manifest *manifestpkg.PodManifest, statuses map[string]serviceStatus, statusErr string) topologyPageData {
	proxyByType := make(map[string]string, len(manifest.Proxies))
	proxyNames := make([]string, 0, len(manifest.Proxies))
	for _, proxy := range manifest.Proxies {
		proxyByType[proxy.ProxyType] = proxy.ServiceName
		proxyNames = append(proxyNames, proxy.ServiceName)
	}
	sort.Strings(proxyNames)

	agentNames := make([]string, 0)
	channelSet := map[string]struct{}{}
	serviceSet := map[string]struct{}{}
	volumeSet := map[string]struct{}{}
	edgeDefs := make([]edgeDef, 0)

	serviceNames := sortedServiceNames(manifest.Services)
	for _, serviceName := range serviceNames {
		svc := manifest.Services[serviceName]
		if svc.ClawType != "" {
			agentNames = append(agentNames, serviceName)
		} else {
			serviceSet[serviceName] = struct{}{}
		}
	}
	sort.Strings(agentNames)

	for _, agent := range agentNames {
		svc := manifest.Services[agent]
		for _, surface := range svc.Surfaces {
			switch surface.Scheme {
			case "channel":
				channelSet[surface.Target] = struct{}{}
				edgeDefs = append(edgeDefs, edgeDef{
					fromLane: "channel", fromName: surface.Target,
					toLane: "agent", toName: agent,
					kind: "channel",
				})
			case "service":
				serviceSet[surface.Target] = struct{}{}
				edgeDefs = append(edgeDefs, edgeDef{
					fromLane: "agent", fromName: agent,
					toLane: "service", toName: surface.Target,
					kind: "service",
				})
			case "volume":
				volumeSet[surface.Target] = struct{}{}
				edgeDefs = append(edgeDefs, edgeDef{
					fromLane: "agent", fromName: agent,
					toLane: "volume", toName: surface.Target,
					kind: "volume",
				})
			case "host":
				hostTarget := "host:" + surface.Target
				volumeSet[hostTarget] = struct{}{}
				edgeDefs = append(edgeDefs, edgeDef{
					fromLane: "agent", fromName: agent,
					toLane: "volume", toName: hostTarget,
					kind: "volume",
				})
			}
		}

		for _, proxyType := range svc.Cllama {
			proxyService := proxyByType[proxyType]
			if proxyService == "" {
				proxyService = "cllama-" + proxyType
				if proxyService != "cllama-" {
					proxyNames = append(proxyNames, proxyService)
				}
			}
			edgeDefs = append(edgeDefs, edgeDef{
				fromLane: "agent", fromName: agent,
				toLane: "proxy", toName: proxyService,
				kind: "proxy",
			})
		}
	}

	proxyNames = uniqueSorted(proxyNames)
	channels := sortedSet(channelSet)
	services := sortedSet(serviceSet)
	volumes := sortedSet(volumeSet)

	const (
		nodeW     = 172
		nodeH     = 44
		xStart    = 24
		yStart    = 52
		laneGap   = 220
		rowGap    = 68
		canvasPad = 36
		minRows   = 3
		laneCount = 5
	)

	laneX := map[string]int{
		"channel": xStart + laneGap*0,
		"agent":   xStart + laneGap*1,
		"proxy":   xStart + laneGap*2,
		"service": xStart + laneGap*3,
		"volume":  xStart + laneGap*4,
	}

	type laneNodes struct {
		lane  string
		names []string
	}
	lanes := []laneNodes{
		{lane: "channel", names: channels},
		{lane: "agent", names: agentNames},
		{lane: "proxy", names: proxyNames},
		{lane: "service", names: services},
		{lane: "volume", names: volumes},
	}

	nodeMap := make(map[string]topologyNode)
	nodes := make([]topologyNode, 0)
	maxRows := minRows
	for _, lane := range lanes {
		if len(lane.names) > maxRows {
			maxRows = len(lane.names)
		}
		for row, name := range lane.names {
			serviceName := ""
			switch lane.lane {
			case "agent", "proxy", "service":
				serviceName = name
			}
			status := statuses[serviceName]
			if strings.TrimSpace(status.Status) == "" {
				status = unknownStatus(serviceName)
			}
			if serviceName == "" {
				status.Status = "n/a"
				status.Uptime = "-"
			}

			node := topologyNode{
				ID:          topologyNodeID(lane.lane, name),
				Label:       name,
				Lane:        lane.lane,
				ServiceName: serviceName,
				X:           laneX[lane.lane],
				Y:           yStart + row*rowGap,
				Width:       nodeW,
				Height:      nodeH,
				Status:      status.Status,
				StatusClass: statusClass(status.Status),
			}
			nodes = append(nodes, node)
			nodeMap[nodeKey(lane.lane, name)] = node
		}
	}

	neighborMap := make(map[string]map[string]struct{})
	edges := make([]topologyEdge, 0)
	seenEdges := make(map[string]struct{})
	for _, edge := range edgeDefs {
		from, okFrom := nodeMap[nodeKey(edge.fromLane, edge.fromName)]
		to, okTo := nodeMap[nodeKey(edge.toLane, edge.toName)]
		if !okFrom || !okTo {
			continue
		}

		key := from.ID + ">" + to.ID + ":" + edge.kind
		if _, exists := seenEdges[key]; exists {
			continue
		}
		seenEdges[key] = struct{}{}

		x1 := from.X + from.Width
		y1 := from.Y + from.Height/2
		x2 := to.X
		y2 := to.Y + to.Height/2
		mid := (x1 + x2) / 2

		edges = append(edges, topologyEdge{
			FromID: from.ID,
			ToID:   to.ID,
			Path:   fmt.Sprintf("M %d %d C %d %d, %d %d, %d %d", x1, y1, mid, y1, mid, y2, x2, y2),
			Color:  topologyEdgeColor(edge.kind),
		})

		if neighborMap[from.ID] == nil {
			neighborMap[from.ID] = map[string]struct{}{}
		}
		if neighborMap[to.ID] == nil {
			neighborMap[to.ID] = map[string]struct{}{}
		}
		neighborMap[from.ID][to.ID] = struct{}{}
		neighborMap[to.ID][from.ID] = struct{}{}
	}

	for i := range nodes {
		neighbors := sortedSet(neighborMap[nodes[i].ID])
		nodes[i].Neighbors = strings.Join(neighbors, ",")
	}

	canvasWidth := xStart + laneGap*(laneCount-1) + nodeW + canvasPad
	canvasHeight := yStart + maxRows*rowGap + canvasPad
	if canvasHeight < 300 {
		canvasHeight = 300
	}

	return topologyPageData{
		PodName:         manifest.PodName,
		ActiveTab:       "topology",
		CanvasWidth:     canvasWidth,
		CanvasHeight:    canvasHeight,
		Nodes:           nodes,
		Edges:           edges,
		StatusError:     statusErr,
		HasStatusErrors: statusErr != "",
	}
}

func topologyNodeID(lane, name string) string {
	safe := strings.ToLower(strings.TrimSpace(name))
	replacer := strings.NewReplacer(" ", "-", "/", "-", ":", "-", ".", "-", "_", "-")
	safe = replacer.Replace(safe)
	return lane + "-" + safe
}

func nodeKey(lane, name string) string {
	return lane + "|" + name
}

func topologyEdgeColor(kind string) string {
	switch kind {
	case "channel":
		return "var(--cyan)"
	case "service":
		return "var(--amber)"
	case "volume":
		return "var(--green)"
	case "proxy":
		return "var(--purple)"
	default:
		return "var(--line-bright)"
	}
}

func sortedSet(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func uniqueSorted(items []string) []string {
	set := make(map[string]struct{}, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		set[item] = struct{}{}
	}
	return sortedSet(set)
}
