package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestAcceptanceProjectLifecycle(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("set TF_ACC=1")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("authorization") != "Bearer evk_test" {
			w.WriteHeader(403)
			return
		}
		switch r.Method {
		case "POST":
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"project":{"id":"flag_project:p","key":"shop","name":"Shop"}}`))
		case "GET":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "flag_project:p", "key": "shop", "name": "Shop"})
		case "PUT", "DELETE":
			w.WriteHeader(204)
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, "evk_test")
	ctx := context.Background()
	if _, err := client.Do(ctx, "POST", "/projects", map[string]any{"key": "shop", "name": "Shop"}, nil); err != nil {
		t.Fatal(err)
	}
	var project map[string]any
	if _, err := client.Do(ctx, "GET", "/projects/shop", nil, &project); err != nil {
		t.Fatal(err)
	}
	if project["key"] != "shop" {
		t.Fatalf("unexpected project: %#v", project)
	}
	if _, err := client.Do(ctx, "PUT", "/projects/shop", map[string]any{"name": "Shop 2"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Do(ctx, "DELETE", "/projects/shop", nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestApprovalReturnsPending(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{"pending":true,"changeId":"flag_scheduled_change:c"}`))
	}))
	defer server.Close()
	client := NewClient(server.URL, "evk_test")
	status, err := client.Do(context.Background(), "PUT", "/projects/shop/flags/checkout", map[string]any{"targeting": map[string]any{}}, nil)
	if err != nil || status != 202 {
		t.Fatalf("status=%d err=%v", status, err)
	}
}
