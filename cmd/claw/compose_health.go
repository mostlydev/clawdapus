package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/spf13/cobra"

	"github.com/mostlydev/clawdapus/internal/driver"
	_ "github.com/mostlydev/clawdapus/internal/driver/nanoclaw"
	_ "github.com/mostlydev/clawdapus/internal/driver/openclaw"
)

var composeHealthCmd = &cobra.Command{
	Use:   "health",
	Short: "Show health status of Claw pod containers",
	RunE: func(cmd *cobra.Command, args []string) error {
		generatedPath, err := resolveComposeGeneratedPath()
		if err != nil {
			return err
		}

		// Get all container IDs from compose
		out, err := exec.Command("docker", "compose", "-f", generatedPath, "ps", "-q").Output()
		if err != nil {
			return fmt.Errorf("docker compose ps: %w", err)
		}

		ids := strings.Fields(strings.TrimSpace(string(out)))
		if len(ids) == 0 {
			fmt.Println("No running containers found.")
			return nil
		}

		cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
		if err != nil {
			return fmt.Errorf("docker client: %w", err)
		}
		defer cli.Close()

		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "SERVICE\tSTATUS\tDETAIL")

		for _, id := range ids {
			info, err := cli.ContainerInspect(context.Background(), id)
			if err != nil {
				fmt.Fprintf(w, "%s\t%s\t%s\n", shortContainerID(id), "error", fmt.Sprintf("inspect failed: %v", err))
				continue
			}

			labels := map[string]string{}
			if info.Config != nil && info.Config.Labels != nil {
				labels = info.Config.Labels
			}

			serviceName := labels["claw.service"]
			if serviceName == "" {
				serviceName = labels["com.docker.compose.service"]
			}
			if serviceName == "" {
				serviceName = shortContainerID(id)
			}

			clawType := labels["claw.type"]
			if clawType == "" {
				status, detail := nativeContainerStatus(info)
				fmt.Fprintf(w, "%s\t%s\t%s\n", serviceName, status, detail)
				continue
			}

			d, err := driver.Lookup(clawType)
			if err != nil {
				fmt.Fprintf(w, "%s\t%s\t%s\n", serviceName, "error", fmt.Sprintf("unknown driver: %s", clawType))
				continue
			}

			h, err := d.HealthProbe(driver.ContainerRef{
				ContainerID: id,
				ServiceName: serviceName,
			})
			if err != nil {
				fmt.Fprintf(w, "%s\t%s\t%s\n", serviceName, "error", err.Error())
				continue
			}

			status := "unhealthy"
			if h.OK {
				status = "healthy"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", serviceName, status, h.Detail)
		}

		w.Flush()
		return nil
	},
}

func nativeContainerStatus(info types.ContainerJSON) (string, string) {
	if info.State == nil {
		return "unknown", "state unavailable"
	}

	if info.State.Health != nil && info.State.Health.Status != "" {
		return info.State.Health.Status, "native docker healthcheck"
	}

	if info.State.Running {
		return "running", "native (no claw driver)"
	}

	if info.State.Status != "" {
		return info.State.Status, "native (no claw driver)"
	}

	return "unknown", "native (no claw driver)"
}

func shortContainerID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

func init() {
	rootCmd.AddCommand(composeHealthCmd)
}
