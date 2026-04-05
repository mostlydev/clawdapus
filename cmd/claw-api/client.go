package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mostlydev/clawdapus/internal/clawapi"
)

func runLocalRequest(cfg config, stdout io.Writer, method, requestPath, requestBody, principalName string, timeout time.Duration) error {
	store, err := clawapi.LoadStore(cfg.PrincipalsPath)
	if err != nil {
		return err
	}
	token, err := principalTokenByName(store, principalName)
	if err != nil {
		return err
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	bodyReader := io.Reader(nil)
	trimmedBody := strings.TrimSpace(requestBody)
	if trimmedBody != "" {
		bodyReader = strings.NewReader(trimmedBody)
	}
	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(strings.TrimSpace(method)), localAPIURL(cfg.Addr, requestPath), bodyReader)
	if err != nil {
		return fmt.Errorf("build local request: %w", err)
	}
	if trimmedBody != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return fmt.Errorf("execute local request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read local response: %w", err)
	}
	if err := writeFormattedResponse(stdout, body); err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return quietExitError{}
	}
	return nil
}

func principalTokenByName(store *clawapi.Store, principalName string) (string, error) {
	if store == nil {
		return "", fmt.Errorf("principal store not configured")
	}
	principalName = strings.TrimSpace(principalName)
	if principalName == "" {
		return "", fmt.Errorf("principal name must not be empty")
	}
	for _, principal := range store.Principals {
		if strings.TrimSpace(principal.Name) == principalName {
			return principal.Token, nil
		}
	}
	return "", fmt.Errorf("principal %q not found", principalName)
}

func localAPIURL(addr, requestPath string) string {
	base := strings.TrimSuffix(healthcheckURL(addr), "/health")
	requestPath = strings.TrimSpace(requestPath)
	if requestPath == "" {
		requestPath = "/"
	} else if !strings.HasPrefix(requestPath, "/") {
		requestPath = "/" + requestPath
	}
	return base + requestPath
}

func writeFormattedResponse(stdout io.Writer, body []byte) error {
	if stdout == nil || len(body) == 0 {
		return nil
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, trimmed, "", "  ") == nil {
		pretty.WriteByte('\n')
		_, err := stdout.Write(pretty.Bytes())
		return err
	}
	if !bytes.HasSuffix(body, []byte{'\n'}) {
		body = append(body, '\n')
	}
	_, err := stdout.Write(body)
	return err
}
