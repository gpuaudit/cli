// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseResponse_ExtractsByLabel(t *testing.T) {
	data := []byte(`{
		"status": "success",
		"data": {
			"resultType": "vector",
			"result": [
				{"metric": {"Hostname": "ip-10-0-1-1"}, "value": [1700000000, "45.2"]},
				{"metric": {"Hostname": "ip-10-0-1-2"}, "value": [1700000000, "12.8"]}
			]
		}
	}`)

	results, err := ParseResponse(data, "Hostname")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results["ip-10-0-1-1"] != 45.2 {
		t.Errorf("expected 45.2, got %f", results["ip-10-0-1-1"])
	}
	if results["ip-10-0-1-2"] != 12.8 {
		t.Errorf("expected 12.8, got %f", results["ip-10-0-1-2"])
	}
}

func TestParseResponse_SkipsMissingLabel(t *testing.T) {
	data := []byte(`{
		"status": "success",
		"data": {
			"resultType": "vector",
			"result": [
				{"metric": {"other": "value"}, "value": [1700000000, "45.2"]}
			]
		}
	}`)

	results, err := ParseResponse(data, "Hostname")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestParseResponse_ErrorStatus(t *testing.T) {
	data := []byte(`{"status": "error", "errorType": "bad_data", "error": "parse error"}`)

	_, err := ParseResponse(data, "node")
	if err == nil {
		t.Error("expected error for non-success status")
	}
}

func TestQueryHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		query := r.URL.Query().Get("query")
		if query == "" {
			t.Error("expected query parameter")
		}
		fmt.Fprintf(w, `{
			"status": "success",
			"data": {
				"resultType": "vector",
				"result": [
					{"metric": {"node": "host1"}, "value": [1700000000, "55.5"]}
				]
			}
		}`)
	}))
	defer srv.Close()

	results, err := QueryHTTP(context.Background(), srv.URL, "up", "node")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results["host1"] != 55.5 {
		t.Errorf("expected 55.5, got %f", results["host1"])
	}
}
