package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

const piResumeReadinessTimeout = 30 * time.Second

var piResumeExpectedModels = map[string]bool{
	"void-codex/gpt-6-astra":   true,
	"void-codex/gpt-5.6-sol":   true,
	"void-codex/gpt-5.6-terra": true,
	"void-codex/gpt-5.6-luna":  true,
}

func sendPiRPC(stdin io.Writer, id, command string, fields map[string]any) error {
	request := map[string]any{"id": id, "type": command}
	for key, value := range fields {
		request[key] = value
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdin, "%s\n", encoded)
	return err
}

func waitForPiRPCResponse(ctx context.Context, records <-chan map[string]json.RawMessage, readErrors <-chan error, stderr *bytes.Buffer, id string) (map[string]json.RawMessage, error) {
	for {
		select {
		case record, ok := <-records:
			if !ok {
				return nil, fmt.Errorf("pinned Pi closed RPC stdout waiting for %s; stderr=%s", id, stderr.String())
			}
			var kind, responseID string
			_ = json.Unmarshal(record["type"], &kind)
			_ = json.Unmarshal(record["id"], &responseID)
			if kind == "response" && responseID == id {
				return record, nil
			}
		case err := <-readErrors:
			if err != nil {
				return nil, fmt.Errorf("invalid pinned Pi RPC JSONL waiting for %s: %v; stderr=%s", id, err, stderr.String())
			}
		case <-ctx.Done():
			return nil, fmt.Errorf("timed out waiting for pinned Pi RPC response %s: %v; stderr=%s", id, ctx.Err(), stderr.String())
		}
	}
}

func piRPCResponseError(record map[string]json.RawMessage, command string) error {
	var success bool
	_ = json.Unmarshal(record["success"], &success)
	var gotCommand string
	_ = json.Unmarshal(record["command"], &gotCommand)
	if !success || gotCommand != command {
		return fmt.Errorf("RPC %s response = %s", command, mustJSON(record))
	}
	return nil
}

func piRPCModels(record map[string]json.RawMessage) (map[string]bool, error) {
	if err := piRPCResponseError(record, "get_available_models"); err != nil {
		return nil, err
	}
	var data struct {
		Models []struct {
			Provider string `json:"provider"`
			ID       string `json:"id"`
		} `json:"models"`
	}
	if err := json.Unmarshal(record["data"], &data); err != nil {
		return nil, fmt.Errorf("decode get_available_models data: %w; response=%s", err, mustJSON(record))
	}
	seen := make(map[string]bool, len(data.Models))
	for _, model := range data.Models {
		seen[model.Provider+"/"+model.ID] = true
	}
	return seen, nil
}

func waitForPiVoidCodexModels(parent context.Context, stdin io.Writer, records <-chan map[string]json.RawMessage, readErrors <-chan error, stderr *bytes.Buffer, prefix string) (map[string]bool, int, error) {
	ctx, cancel := context.WithTimeout(parent, piResumeReadinessTimeout)
	defer cancel()
	last := "<no response>"
	for attempt := 1; ; attempt++ {
		id := fmt.Sprintf("%s-%d", prefix, attempt)
		if err := sendPiRPC(stdin, id, "get_available_models", nil); err != nil {
			return nil, attempt, fmt.Errorf("send readiness RPC %s: %w; stderr=%s", id, err, stderr.String())
		}
		response, err := waitForPiRPCResponse(ctx, records, readErrors, stderr, id)
		if err != nil {
			return nil, attempt, fmt.Errorf("V2 authority readiness failed after %d poll(s): %w; last=%s", attempt, err, last)
		}
		last = mustJSON(response)
		models, err := piRPCModels(response)
		if err != nil {
			return nil, attempt, fmt.Errorf("V2 authority readiness returned an invalid model response on poll %d: %w; last=%s", attempt, err, last)
		}
		missing := make([]string, 0)
		for model := range piResumeExpectedModels {
			if !models[model] {
				missing = append(missing, model)
			}
		}
		if len(missing) == 0 {
			return models, attempt, nil
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			return nil, attempt, fmt.Errorf("V2 authority readiness timed out after %d poll(s), missing=%s; last=%s; stderr=%s", attempt, strings.Join(missing, ", "), last, stderr.String())
		}
	}
}

func waitForPiAgentSettled(ctx context.Context, records <-chan map[string]json.RawMessage, readErrors <-chan error, stderr *bytes.Buffer) ([]map[string]json.RawMessage, error) {
	var observed []map[string]json.RawMessage
	for {
		select {
		case record, ok := <-records:
			if !ok {
				return observed, fmt.Errorf("pinned Pi closed RPC stdout before agent_settled; stderr=%s", stderr.String())
			}
			observed = append(observed, record)
			var kind string
			_ = json.Unmarshal(record["type"], &kind)
			if kind == "agent_settled" {
				return observed, nil
			}
		case err := <-readErrors:
			if err != nil {
				return observed, fmt.Errorf("invalid pinned Pi RPC JSONL before agent_settled: %v; stderr=%s", err, stderr.String())
			}
		case <-ctx.Done():
			return observed, fmt.Errorf("timed out waiting for pinned Pi agent_settled: %v; stderr=%s", ctx.Err(), stderr.String())
		}
	}
}
