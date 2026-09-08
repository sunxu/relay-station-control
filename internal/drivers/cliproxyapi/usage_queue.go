// HTTP queue envelope adapted from CPA Manager Plus httpqueue/client.go.
// Copyright (c) 2026 Seakee, MIT; see ../../requestquality/CPA-MIT-LICENSE.txt.
package cliproxyapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/sunxu/relay-station-control/internal/drivers"
	"github.com/sunxu/relay-station-control/internal/requestquality"
)

const usageQueueBatchSize = 100

var _ requestquality.Source = (*Driver)(nil)

// PopUsage removes one bounded batch from CPA's destructive usage queue.
func (driver *Driver) PopUsage(ctx context.Context, target drivers.NodeTarget) ([][]byte, error) {
	if driver == nil || ctx == nil {
		return nil, errors.New("usage queue request rejected")
	}
	if reason := validateTarget(target, drivers.CapabilityManagementAccountInventoryRead); reason != drivers.ReasonNone {
		return nil, fmt.Errorf("usage queue target rejected: %s", reason)
	}
	transport, err := driver.transport(target.ManagementEndpoint)
	if err != nil {
		return nil, err
	}
	secret, err := driver.secretResolver.Resolve(ctx, target.ReaderSecretReference)
	if err != nil {
		return nil, err
	}
	defer secret.Destroy()
	var response *httpResponse
	err = secret.Use(func(key string) error {
		response, err = transport.getUsageQueue(ctx, key, usageQueueBatchSize)
		return err
	})
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("usage queue HTTP status %d", response.StatusCode)
	}
	var entries []json.RawMessage
	body, err := io.ReadAll(io.LimitReader(response.Body, driver.inventoryLimits.MaxBodyBytes+1))
	if err != nil || int64(len(body)) > driver.inventoryLimits.MaxBodyBytes {
		return nil, errors.New("usage queue response too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&entries); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("usage queue response has trailing JSON")
	}
	if entries == nil || len(entries) > usageQueueBatchSize {
		return nil, errors.New("usage queue response invalid")
	}
	result := make([][]byte, 0, len(entries))
	for _, entry := range entries {
		entry = bytes.TrimSpace(entry)
		if len(entry) == 0 || bytes.Equal(entry, []byte("null")) {
			continue
		}
		if entry[0] == '"' {
			var text string
			if err := json.Unmarshal(entry, &text); err != nil || strings.TrimSpace(text) == "" {
				return nil, errors.New("usage queue item invalid")
			}
			entry = []byte(text)
		}
		result = append(result, append([]byte(nil), entry...))
	}
	return result, nil
}

// CurrentIdentities reads the existing auth-files endpoint and returns only
// identity evidence needed by the request-quality collector.
func (driver *Driver) CurrentIdentities(ctx context.Context, target drivers.NodeTarget) ([]requestquality.Identity, error) {
	if driver == nil || ctx == nil {
		return nil, errors.New("identity request rejected")
	}
	if reason := validateTarget(target, drivers.CapabilityManagementAccountInventoryRead); reason != drivers.ReasonNone {
		return nil, fmt.Errorf("identity target rejected: %s", reason)
	}
	transport, err := driver.transport(target.ManagementEndpoint)
	if err != nil {
		return nil, err
	}
	secret, err := driver.secretResolver.Resolve(ctx, target.ReaderSecretReference)
	if err != nil {
		return nil, err
	}
	defer secret.Destroy()
	var response *httpResponse
	err = secret.Use(func(key string) error { response, err = transport.getAccountInventory(ctx, key); return err })
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("auth-files HTTP status %d", response.StatusCode)
	}
	var top struct {
		Files []map[string]json.RawMessage `json:"files"`
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, driver.inventoryLimits.MaxBodyBytes+1))
	if err != nil || int64(len(body)) > driver.inventoryLimits.MaxBodyBytes {
		return nil, errors.New("auth-files response too large")
	}
	if err := json.Unmarshal(body, &top); err != nil || top.Files == nil || len(top.Files) > driver.inventoryLimits.MaxRecords {
		return nil, errors.New("auth-files response invalid")
	}
	result := make([]requestquality.Identity, 0, len(top.Files))
	for _, file := range top.Files {
		// Only explicit email is identity evidence. Account snapshots may be
		// labels, API keys, or provider-specific values and are never guessed as
		// an email/account identity here.
		result = append(result, requestquality.Identity{
			AuthIndex: rawString(file, "auth_index", "authIndex", "AuthIndex"),
			Provider:  rawString(file, "provider", "type", "auth_type", "authType"),
			Email:     rawString(file, "email"),
		})
	}
	return result, nil
}

func rawString(fields map[string]json.RawMessage, names ...string) string {
	for _, name := range names {
		if raw, ok := fields[name]; ok {
			var value string
			if json.Unmarshal(raw, &value) == nil && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}
