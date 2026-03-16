package shared

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/docker/docker/client"
)

func TestExecInContainer(t *testing.T) {
	stream := append(stdcopyFrame(1, "stdout line\n"), stdcopyFrame(2, "stderr line\n")...)

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/containers/container-123/exec"):
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"Id":"exec-123"}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/exec/exec-123/start"):
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("response writer does not support hijacking")
			}
			conn, buf, err := hj.Hijack()
			if err != nil {
				t.Fatalf("hijack response: %v", err)
			}
			defer conn.Close()

			fmt.Fprintf(buf, "HTTP/1.1 101 UPGRADED\r\n")
			fmt.Fprintf(buf, "Content-Type: application/vnd.docker.raw-stream\r\n")
			fmt.Fprintf(buf, "Connection: Upgrade\r\n")
			fmt.Fprintf(buf, "Upgrade: tcp\r\n\r\n")
			if _, err := buf.Write(stream); err != nil {
				t.Fatalf("write stream: %v", err)
			}
			if err := buf.Flush(); err != nil {
				t.Fatalf("flush stream: %v", err)
			}
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/exec/exec-123/json"):
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"ID":"exec-123","Running":false,"ExitCode":7}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}

	cli, err := client.NewClientWithOpts(
		client.WithHost("tcp://"+serverURL.Host),
		client.WithVersion("1.44"),
		client.WithHTTPClient(server.Client()),
	)
	if err != nil {
		t.Fatalf("create docker client: %v", err)
	}
	defer cli.Close()

	stdout, stderr, exitCode, err := ExecInContainer(context.Background(), cli, "container-123", []string{"echo", "hi"})
	if err != nil {
		t.Fatalf("ExecInContainer returned error: %v", err)
	}
	if stdout != "stdout line\n" {
		t.Fatalf("unexpected stdout: %q", stdout)
	}
	if stderr != "stderr line\n" {
		t.Fatalf("unexpected stderr: %q", stderr)
	}
	if exitCode != 7 {
		t.Fatalf("unexpected exit code: %d", exitCode)
	}
}

func stdcopyFrame(stream byte, payload string) []byte {
	frame := make([]byte, 8+len(payload))
	frame[0] = stream
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(payload)))
	copy(frame[8:], payload)
	return frame
}
