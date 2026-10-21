package pod

import (
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDashboardPublication(t *testing.T) {
	for _, tc := range []struct {
		bind, proxyPort, dashPort, url string
	}{
		{"", "8181:8081", "8082:8082", "http://127.0.0.1:8181"},
		{"127.0.0.1", "127.0.0.1:8181:8081", "127.0.0.1:8082:8082", "http://127.0.0.1:8181"},
		{"192.0.2.1", "192.0.2.1:8181:8081", "192.0.2.1:8082:8082", "http://192.0.2.1:8181"},
		{"[::1]", "[::1]:8181:8081", "[::1]:8082:8082", "http://[::1]:8181"},
		{"::", "[::]:8181:8081", "[::]:8082:8082", "http://[::1]:8181"},
		{"0.0.0.0", "0.0.0.0:8181:8081", "0.0.0.0:8082:8082", "http://127.0.0.1:8181"},
	} {
		t.Run(tc.bind, func(t *testing.T) {
			p := &Pod{Services: map[string]*Service{}, Clawdash: &ClawdashConfig{
				Image: "dashboard:test", Addr: ":8082", HostBind: tc.bind, ManifestHostPath: "/runtime/manifest.json",
			}}
			proxy := CllamaProxyConfig{ProxyType: "passthrough", Image: "proxy:test", ContextHostDir: "/runtime/context", AuthHostDir: "/auth", DashboardBind: tc.bind}
			out, err := EmitCompose(p, nil, proxy)
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Services map[string]struct {
					Ports       []string          `yaml:"ports"`
					Environment map[string]string `yaml:"environment"`
				} `yaml:"services"`
			}
			if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
				t.Fatal(err)
			}
			if got := doc.Services["cllama"].Ports; !reflect.DeepEqual(got, []string{tc.proxyPort}) {
				t.Fatalf("proxy publication = %v", got)
			}
			if got := doc.Services["clawdash"].Ports; !reflect.DeepEqual(got, []string{tc.dashPort}) {
				t.Fatalf("dashboard publication = %v", got)
			}
			if got := doc.Services["clawdash"].Environment["CLAWDASH_ADDR"]; got != ":8082" {
				t.Fatalf("host binding changed container listener: %q", got)
			}
			bind, _ := NormalizeDashboardBind(tc.bind)
			if got := DashboardURL(bind, "8181"); got != tc.url {
				t.Fatalf("host revoke URL = %q, want %q", got, tc.url)
			}
			p.DisableClawdash = true
			out, err = EmitCompose(p, nil, proxy)
			if err != nil {
				t.Fatal(err)
			}
			doc.Services = nil
			if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
				t.Fatal(err)
			}
			if _, ok := doc.Services["clawdash"]; ok {
				t.Fatal("disabled clawdash still emitted")
			}
			if len(doc.Services["cllama"].Ports) != 1 {
				t.Fatal("disabling dashboard removed revocation endpoint")
			}
		})
	}
}

func TestDashboardSettingsRejectAmbiguousAddresses(t *testing.T) {
	for _, bind := range []string{"localhost", "127.0.0.1:8181", "[::1", "::1]", "bad"} {
		if _, err := NormalizeDashboardBind(bind); err == nil {
			t.Errorf("accepted bind %q", bind)
		}
	}
	for _, addr := range []string{"127.0.0.1:8082", "[::1]:8082", "localhost:8082", ":0", ":bad"} {
		if _, err := ClawdashListenAddress(addr); err == nil {
			t.Errorf("accepted container address %q", addr)
		}
	}
}
