package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/mostlydev/clawdapus/internal/infraimages"
)

func main() {
	var releaseTag string
	flag.StringVar(&releaseTag, "release-tag", "", "release tag to validate (for example v0.7.0)")
	flag.Parse()

	releaseTag = strings.TrimSpace(releaseTag)
	if releaseTag == "" {
		fmt.Fprintln(os.Stderr, "missing --release-tag")
		os.Exit(2)
	}

	var missing []string
	for _, ref := range infraimages.ReleaseRefs(releaseTag) {
		cmd := exec.Command("docker", "manifest", "inspect", ref)
		out, err := cmd.CombinedOutput()
		if err != nil {
			detail := strings.TrimSpace(string(out))
			if detail == "" {
				detail = err.Error()
			}
			fmt.Fprintf(os.Stderr, "missing %s: %s\n", ref, detail)
			missing = append(missing, ref)
			continue
		}
		fmt.Printf("verified %s\n", ref)
	}

	if len(missing) > 0 {
		os.Exit(1)
	}
}
