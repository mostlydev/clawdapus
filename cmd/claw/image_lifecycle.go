package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mostlydev/clawdapus/internal/driver/hermes"
	"github.com/mostlydev/clawdapus/internal/infraimages"
	"github.com/mostlydev/clawdapus/internal/inspect"
	"github.com/mostlydev/clawdapus/internal/pod"
)

const (
	infraComponentClawAPI      = "claw-api"
	infraComponentClawdash     = "clawdash"
	infraComponentClawWall     = "claw-wall"
	infraComponentCllama       = "cllama"
	infraComponentCllamaPolicy = "cllama-policy"
	infraComponentHermesBase   = "hermes-base"
)

var (
	infraClawAPITag    = infraimages.DefaultClawAPITag
	infraClawdashTag   = infraimages.DefaultClawdashTag
	infraClawWallTag   = infraimages.DefaultClawWallTag
	infraCllamaTag     = infraimages.DefaultCllamaTag
	infraHermesBaseTag = infraimages.DefaultHermesBaseTag
)

type infraImageSpec struct {
	Component      string
	ExpectedRef    string
	DockerfilePath string
	ContextDir     string
}

type plannedServiceImage struct {
	ServiceName string
	ImageRef    string
	BuildConfig *serviceBuildConfig
	Present     bool
}

func init() {
	hermes.BaseImageTag = preferredInfraImageRef(infraComponentHermesBase)
}

func coreInfraImageSpecs() []infraImageSpec {
	return []infraImageSpec{
		{
			Component:      infraComponentClawAPI,
			ExpectedRef:    taggedInfraRef("ghcr.io/mostlydev/claw-api", infraClawAPITag),
			DockerfilePath: "dockerfiles/claw-api/Dockerfile",
			ContextDir:     ".",
		},
		{
			Component:      infraComponentClawdash,
			ExpectedRef:    taggedInfraRef("ghcr.io/mostlydev/clawdash", infraClawdashTag),
			DockerfilePath: "dockerfiles/clawdash/Dockerfile",
			ContextDir:     ".",
		},
		{
			Component:      infraComponentClawWall,
			ExpectedRef:    taggedInfraRef("ghcr.io/mostlydev/claw-wall", infraClawWallTag),
			DockerfilePath: conversationWallDockerfile,
			ContextDir:     ".",
		},
		{
			Component:      infraComponentCllama,
			ExpectedRef:    taggedInfraRef("ghcr.io/mostlydev/cllama", infraCllamaTag),
			DockerfilePath: "cllama/Dockerfile",
			ContextDir:     "cllama",
		},
	}
}

func allInfraImageSpecs() []infraImageSpec {
	specs := append([]infraImageSpec(nil), coreInfraImageSpecs()...)
	specs = append(specs, infraImageSpec{
		Component:      infraComponentCllamaPolicy,
		ExpectedRef:    taggedInfraRef("ghcr.io/mostlydev/cllama-policy", infraCllamaTag),
		DockerfilePath: "cllama/Dockerfile",
		ContextDir:     "cllama",
	})
	specs = append(specs, infraImageSpec{
		Component:      infraComponentHermesBase,
		ExpectedRef:    taggedInfraRef("ghcr.io/mostlydev/hermes-base", infraHermesBaseTag),
		DockerfilePath: "dockerfiles/hermes-base/Dockerfile",
		ContextDir:     "dockerfiles/hermes-base",
	})
	return specs
}

func taggedInfraRef(repo, tag string) string {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return ""
	}
	return repo + ":" + tag
}

func infraImageSpecFor(component string) infraImageSpec {
	for _, spec := range allInfraImageSpecs() {
		if spec.Component == component {
			return spec
		}
	}
	return infraImageSpec{}
}

func preferredInfraImageRef(component string) string {
	spec := infraImageSpecFor(component)
	return strings.TrimSpace(spec.ExpectedRef)
}

func resolveProxyImageRef(proxyType string) string {
	switch strings.TrimSpace(strings.ToLower(proxyType)) {
	case "", "passthrough":
		return preferredInfraImageRef(infraComponentCllama)
	case "policy":
		spec := infraImageSpecFor(infraComponentCllamaPolicy)
		if strings.TrimSpace(spec.ExpectedRef) != "" {
			return spec.ExpectedRef
		}
	}
	return preferredInfraImageRef(infraComponentCllama)
}

func resolveConversationWallImageRef() string {
	return preferredInfraImageRef(infraComponentClawWall)
}

func resolveOptionalPodFile(explicit string, args []string) (string, bool, error) {
	if explicit != "" && len(args) > 0 {
		return "", false, fmt.Errorf("pod file specified twice: use either '--file %s' or positional arg '%s', not both", explicit, args[0])
	}
	if explicit != "" {
		return explicit, true, nil
	}
	if len(args) > 0 {
		return args[0], true, nil
	}
	if _, err := os.Stat("claw-pod.yml"); err == nil {
		return "claw-pod.yml", true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, fmt.Errorf("stat claw-pod.yml: %w", err)
	}
	return "", false, nil
}

func loadPodDefinition(podFile string) (*pod.Pod, string, error) {
	f, err := os.Open(podFile)
	if err != nil {
		return nil, "", fmt.Errorf("open pod file: %w", err)
	}
	defer f.Close()

	p, err := pod.Parse(f)
	if err != nil {
		return nil, "", err
	}

	podDir, err := filepath.Abs(filepath.Dir(podFile))
	if err != nil {
		return nil, "", fmt.Errorf("resolve pod directory: %w", err)
	}
	return p, podDir, nil
}

func planPodServiceImages(p *pod.Pod) ([]plannedServiceImage, error) {
	if p == nil {
		return nil, nil
	}

	names := make([]string, 0, len(p.Services))
	for name := range p.Services {
		names = append(names, name)
	}
	sort.Strings(names)

	plans := make([]plannedServiceImage, 0, len(names))
	for _, name := range names {
		svc := p.Services[name]
		if svc == nil {
			continue
		}

		imageRef := strings.TrimSpace(svc.Image)
		cfg, err := parseServiceBuildConfig(svc.Compose["build"])
		if err != nil {
			return nil, fmt.Errorf("service %q: parse build: %w", name, err)
		}
		if cfg == nil && imageRef == "" {
			return nil, fmt.Errorf("service %q: services require image: or build:", name)
		}
		if cfg != nil && imageRef == "" {
			imageRef = managedServiceImageRef(p.Name, name)
		}
		assignServiceImageRef(svc, imageRef)

		plans = append(plans, plannedServiceImage{
			ServiceName: name,
			ImageRef:    imageRef,
			BuildConfig: cfg,
			Present:     imageExistsLocally(imageRef),
		})
	}
	return plans, nil
}

func assignServiceImageRef(svc *pod.Service, imageRef string) {
	if svc == nil || strings.TrimSpace(imageRef) == "" {
		return
	}
	svc.Image = imageRef
	if svc.Compose == nil {
		svc.Compose = make(map[string]interface{})
	}
	svc.Compose["image"] = imageRef
}

func pullInfraImagesFromRegistry(specs []infraImageSpec) error {
	for _, spec := range specs {
		if err := pullInfraImageFromRegistry(spec); err != nil {
			return err
		}
	}
	return nil
}

func pullCoreInfraImages() error {
	return pullInfraImagesFromRegistry(coreInfraImageSpecs())
}

// pullInfraImageFromRegistry pulls the infra image from the registry only.
// Unlike ensureInfraImageAvailable, it never falls back to building from
// local source or a git URL — if the tag isn't in the registry, it fails.
func pullInfraImageFromRegistry(spec infraImageSpec) error {
	if _, ok := acceptableLocalInfraRef(spec); ok {
		return nil
	}

	ref := strings.TrimSpace(spec.ExpectedRef)
	if ref == "" {
		return fmt.Errorf("%s image has no pinned ref configured in this claw build", spec.Component)
	}
	if err := runInfraDockerCommand("pull", ref); err == nil {
		return nil
	}

	return fmt.Errorf("%s image %q not available in registry; is the tag published?", spec.Component, ref)
}

func acceptableLocalInfraRef(spec infraImageSpec) (string, bool) {
	if strings.TrimSpace(spec.ExpectedRef) != "" && imageExistsLocally(spec.ExpectedRef) {
		return spec.ExpectedRef, true
	}
	return "", false
}

func pullRegistryServiceImages(plans []plannedServiceImage, pullMissingOnly bool) error {
	for _, plan := range plans {
		if plan.BuildConfig != nil {
			continue
		}
		if pullMissingOnly && plan.Present {
			continue
		}
		fmt.Printf("[claw] pulling service %s image %s\n", plan.ServiceName, plan.ImageRef)
		if err := runInfraDockerCommand("pull", plan.ImageRef); err != nil {
			return fmt.Errorf("service %q: pull image %q: %w", plan.ServiceName, plan.ImageRef, err)
		}
	}
	return nil
}

func requiredPodPullInfraSpecs(podDir string, p *pod.Pod, plans []plannedServiceImage) ([]infraImageSpec, error) {
	if p == nil {
		return nil, nil
	}

	required := make([]infraImageSpec, 0, 4)
	seen := make(map[string]struct{})
	add := func(component string) {
		if _, ok := seen[component]; ok {
			return
		}
		spec := infraImageSpecFor(component)
		if spec.Component == "" {
			return
		}
		required = append(required, spec)
		seen[component] = struct{}{}
	}

	hasManagedServices := false
	for _, svc := range p.Services {
		if svc.IsAgentManaged() {
			hasManagedServices = true
			break
		}
	}
	if hasManagedServices {
		add(infraComponentClawdash)
	}
	if p.Master != "" || hasPodInvokeEntries(p) {
		add(infraComponentClawAPI)
	}

	needsConversationWall := false
	for _, plan := range plans {
		svc := p.Services[plan.ServiceName]
		if !svc.IsAgentManaged() {
			continue
		}

		usesPolicy, err := serviceUsesProxyTypeForPull(podDir, svc, plan, "policy")
		if err != nil {
			return nil, fmt.Errorf("service %q: inspect cllama metadata: %w", plan.ServiceName, err)
		}
		if usesPolicy {
			add(infraComponentCllamaPolicy)
		}
		usesPassthrough, err := serviceUsesProxyTypeForPull(podDir, svc, plan, "passthrough")
		if err != nil {
			return nil, fmt.Errorf("service %q: inspect cllama metadata: %w", plan.ServiceName, err)
		}
		if usesPassthrough {
			add(infraComponentCllama)
		}
		if !usesPolicy && !usesPassthrough && len(svc.Claw.Cllama) > 0 {
			add(infraComponentCllama)
			usesPassthrough = true
		}
		if (usesPolicy || usesPassthrough) && len(discordHandleChannelIDs(svc.Claw.Handles)) > 0 {
			needsConversationWall = true
		}
	}
	if needsConversationWall {
		add(infraComponentClawWall)
	}

	return required, nil
}

func serviceUsesProxyTypeForPull(podDir string, svc *pod.Service, plan plannedServiceImage, proxyType string) (bool, error) {
	if !svc.IsAgentManaged() {
		return false, nil
	}
	if proxyListContains(svc.Claw.Cllama, proxyType) {
		return true, nil
	}

	if strings.TrimSpace(plan.ImageRef) != "" && imageExistsLocally(plan.ImageRef) {
		info, err := inspectClawImage(plan.ImageRef)
		if err != nil {
			return false, err
		}
		return clawInfoUsesProxyType(info, proxyType), nil
	}

	if plan.BuildConfig != nil {
		info, err := inspectBuildMetadata(podDir, svc.Compose["build"])
		if err != nil {
			return false, err
		}
		return clawInfoUsesProxyType(info, proxyType), nil
	}

	return false, nil
}

func clawInfoUsesProxyType(info *inspect.ClawInfo, proxyType string) bool {
	if info == nil {
		return false
	}
	return proxyListContains(info.Cllama, proxyType)
}

func proxyListContains(values []string, target string) bool {
	target = strings.TrimSpace(strings.ToLower(target))
	for _, value := range values {
		if strings.TrimSpace(strings.ToLower(value)) == target {
			return true
		}
	}
	return false
}

func buildPlannedServiceImages(podFile, podDir string, plans []plannedServiceImage, buildMissingOnly bool) error {
	for _, plan := range plans {
		if plan.BuildConfig == nil {
			continue
		}
		if buildMissingOnly && plan.Present {
			continue
		}
		fmt.Printf("[claw] %s: building image %s\n", plan.ServiceName, plan.ImageRef)
		if err := buildManagedServiceImage(podFile, podDir, plan.ImageRef, plan.BuildConfig); err != nil {
			return fmt.Errorf("service %q: %w", plan.ServiceName, err)
		}
	}
	return nil
}

func requiredInfraImageSpecs(p *pod.Pod, cllamaEnabled bool, proxies []pod.CllamaProxyConfig, api *pod.ClawAPIConfig, dash *pod.ClawdashConfig) []infraImageSpec {
	required := make([]infraImageSpec, 0)
	seen := make(map[string]struct{})
	add := func(component string) {
		if _, ok := seen[component]; ok {
			return
		}
		spec := infraImageSpecFor(component)
		if spec.Component == "" {
			return
		}
		required = append(required, spec)
		seen[component] = struct{}{}
	}

	if cllamaEnabled {
		for _, proxy := range proxies {
			switch strings.TrimSpace(strings.ToLower(proxy.ProxyType)) {
			case "", "passthrough":
				add(infraComponentCllama)
			case "policy":
				add(infraComponentCllamaPolicy)
			default:
				add(infraComponentCllama)
			}
		}
		if len(proxies) == 0 {
			add(infraComponentCllama)
		}
	}
	if api != nil {
		add(infraComponentClawAPI)
	}
	if dash != nil {
		add(infraComponentClawdash)
	}
	if p != nil {
		if wall := p.Services[conversationWallServiceName]; wall != nil && strings.TrimSpace(wall.Image) != "" {
			add(infraComponentClawWall)
		}
	}
	return required
}

func ensureRequiredInfraImagesAvailable(specs []infraImageSpec) error {
	for _, spec := range specs {
		if _, ok := acceptableLocalInfraRef(spec); ok {
			continue
		}
		ref := strings.TrimSpace(spec.ExpectedRef)
		if ref == "" {
			ref = spec.Component
		}
		return remediationErrorf("claw pull", "%s image %q missing locally", spec.Component, ref)
	}
	return nil
}

func firstMissingPullPlan(plans []plannedServiceImage) *plannedServiceImage {
	for i := range plans {
		plan := &plans[i]
		if plan.BuildConfig == nil && !plan.Present {
			return plan
		}
	}
	return nil
}

func firstMissingBuildPlan(plans []plannedServiceImage) *plannedServiceImage {
	for i := range plans {
		plan := &plans[i]
		if plan.BuildConfig != nil && !plan.Present {
			return plan
		}
	}
	return nil
}

func remediationErrorf(command, format string, args ...interface{}) error {
	return fmt.Errorf(format+"; run: %s", append(args, command)...)
}
