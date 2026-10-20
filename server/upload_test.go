package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestConditionalCreatesDoNotOverwrite(t *testing.T) {
	var mu sync.Mutex
	files := map[string]string{"/empty.md": ""}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "ai" || p != "test-password" {
			w.WriteHeader(401)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if r.Method == "HEAD" {
			if r.Header.Get("If-None-Match") != "" {
				t.Error("conditional header forwarded to preflight")
			}
			if _, ok := files[r.URL.Path]; !ok {
				w.WriteHeader(404)
			}
			return
		}
		if r.Method == "PUT" {
			b, _ := io.ReadAll(r.Body)
			files[r.URL.Path] = string(b)
			w.WriteHeader(201)
			return
		}
		w.WriteHeader(405)
	}))
	defer upstream.Close()
	g, err := newGateway("https://files.test:8443", upstream.URL, "ai", "test-password")
	if err != nil {
		t.Fatal(err)
	}
	put := func(path, password string) int {
		r := httptest.NewRequest("PUT", "https://files.test:8443"+path, strings.NewReader("new"))
		r.SetBasicAuth("ai", password)
		r.Header.Set("If-None-Match", "*")
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		return w.Code
	}
	if put("/empty.md", "test-password") != 412 {
		t.Fatal("empty existing file overwritten")
	}
	if put("/unauthorized.md", "wrong") != 401 {
		t.Fatal("preflight bypassed auth")
	}
	var workers sync.WaitGroup
	statuses := make(chan int, 8)
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() { defer workers.Done(); statuses <- put("/shared.md", "test-password") }()
	}
	workers.Wait()
	close(statuses)
	created, rejected := 0, 0
	for s := range statuses {
		if s == 201 {
			created++
		} else if s == 412 {
			rejected++
		} else {
			t.Fatalf("unexpected status %d", s)
		}
	}
	if created != 1 || rejected != 7 {
		t.Fatalf("created %d rejected %d", created, rejected)
	}
	mu.Lock()
	defer mu.Unlock()
	if files["/empty.md"] != "" || len(files) != 2 {
		t.Fatal("existing data changed or unauthorized file created")
	}
}
