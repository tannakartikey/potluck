package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testClient(srv *httptest.Server) *Client {
	return &Client{BaseURL: srv.URL, AnonKey: "test-anon", HTTP: srv.Client()}
}

func TestRegisterSendsKeyAndAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/v1/rpc/register_contributor" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("apikey") != "test-anon" || r.Header.Get("Authorization") != "Bearer test-anon" {
			t.Error("missing/incorrect auth headers")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["p_key"] != "potluck_x" {
			t.Errorf("p_key = %v", body["p_key"])
		}
		_, _ = w.Write([]byte(`{"id":"c1","display_name":"alice"}`))
	}))
	defer srv.Close()

	c, err := testClient(srv).Register(context.Background(), "potluck_x", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != "c1" || c.DisplayName != "alice" {
		t.Fatalf("got %+v", c)
	}
}

func TestClaimNullAndObject(t *testing.T) {
	srvNull := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("null"))
	}))
	defer srvNull.Close()
	task, err := testClient(srvNull).Claim(context.Background(), "k", []string{"rails"})
	if err != nil {
		t.Fatal(err)
	}
	if task != nil {
		t.Fatalf("empty queue should yield nil, got %+v", task)
	}

	srvObj := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["p_topics"]; !ok {
			t.Error("expected p_topics in claim body")
		}
		_, _ = w.Write([]byte(`{"id":"s1","title":"T","prompt":"P","acceptance":"A","token_budget":4000,"model_policy":"any","status":"leased"}`))
	}))
	defer srvObj.Close()
	task, err = testClient(srvObj).Claim(context.Background(), "k", []string{"rails"})
	if err != nil {
		t.Fatal(err)
	}
	if task == nil || task.ID != "s1" || task.TokenBudget != 4000 {
		t.Fatalf("got %+v", task)
	}
}

func TestRPCErrorSurfacesMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"row-level security violation","code":"42501"}`))
	}))
	defer srv.Close()
	if _, err := testClient(srv).Submit(context.Background(), "k", "s1", "md", "haiku", 100, "h", true); err == nil || !strings.Contains(err.Error(), "row-level security") {
		t.Fatalf("expected RLS error surfaced, got %v", err)
	}
}
