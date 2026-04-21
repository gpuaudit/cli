// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// QueryHTTP executes a PromQL instant query against a Prometheus-compatible HTTP API
// and returns a map from the given labelName to its metric value.
func QueryHTTP(ctx context.Context, baseURL, query, labelName string) (map[string]float64, error) {
	u := fmt.Sprintf("%s/api/v1/query?query=%s", strings.TrimRight(baseURL, "/"), url.QueryEscape(query))
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return ParseResponse(data, labelName)
}

// ParseResponse extracts metric values from a Prometheus API JSON response,
// keyed by the given label name.
func ParseResponse(data []byte, labelName string) (map[string]float64, error) {
	var resp struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Value  []json.RawMessage `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	if resp.Status != "success" {
		return nil, fmt.Errorf("query returned status %q", resp.Status)
	}

	results := make(map[string]float64)
	for _, r := range resp.Data.Result {
		key := r.Metric[labelName]
		if key == "" || len(r.Value) < 2 {
			continue
		}
		var valStr string
		if err := json.Unmarshal(r.Value[1], &valStr); err != nil {
			continue
		}
		val, err := strconv.ParseFloat(valStr, 64)
		if err != nil {
			continue
		}
		results[key] = val
	}
	return results, nil
}
