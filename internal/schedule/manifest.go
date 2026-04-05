package schedule

type Manifest struct {
	Version     int                  `json:"version"`
	Pod         string               `json:"pod"`
	Invocations []ManifestInvocation `json:"invocations,omitempty"`
}

type ManifestInvocation struct {
	ID       string `json:"id"`
	Service  string `json:"service"`
	AgentID  string `json:"agent_id"`
	Schedule string `json:"schedule"`
	Timezone string `json:"timezone"`
	Message  string `json:"message"`
	Name     string `json:"name,omitempty"`
	To       string `json:"to,omitempty"`
	When     *When  `json:"when,omitempty"`
	Wake     Wake   `json:"wake"`
}

type Wake struct {
	Adapter string   `json:"adapter"`
	Target  string   `json:"target"`
	Command []string `json:"command"`
}
