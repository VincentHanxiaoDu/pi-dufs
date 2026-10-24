package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestDownloadButtonAndAttachment(t *testing.T) {
	page := "<html><body><h1>Hi</h1></BODY></html>"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "ai" || p != "test-password" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if strings.HasSuffix(r.URL.Path, "/list/") {
			w.Write([]byte("<html><body><script>window.__DUFS_PREFIX__='/'</script></body></html>"))
			return
		}
		w.Write([]byte(page))
	}))
	defer upstream.Close()
	g, err := newGateway("https://files.test:8443", upstream.URL, "ai", "test-password")
	if err != nil {
		t.Fatal(err)
	}
	cookie := login(t, g)
	get := func(target, dest string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "https://files.test:8443"+target, nil)
		r.AddCookie(cookie)
		r.Header.Set("Sec-Fetch-Dest", dest)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		return w
	}
	w := get("/docs/net%20map.html", "document")
	body := w.Body.String()
	if !strings.Contains(body, `href="net%20map.html?download" download`) || strings.Index(body, "Download") > strings.Index(body, "</BODY>") {
		t.Fatalf("button missing or misplaced: %s", body)
	}
	if w.Header().Get("Content-Length") != "" && w.Header().Get("Content-Length") != strconv.Itoa(len(body)) {
		t.Fatal("stale Content-Length")
	}
	if !strings.Contains(get("/site/", "document").Body.String(), `href="index.html?download"`) {
		t.Fatal("directory index button missing")
	}
	for _, c := range [][2]string{{"/docs/a.html", "iframe"}, {"/docs/a.html", ""}, {"/docs/a.html?raw=1", "document"}, {"/list/", "document"}} {
		if strings.Contains(get(c[0], c[1]).Body.String(), "Download original file") {
			t.Fatalf("button injected into %v", c)
		}
	}
	w = get("/docs/net%20map.html?download", "document")
	if w.Body.String() != page || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment; filename*=UTF-8''net%20map.html") {
		t.Fatalf("download not original attachment: %q %q", w.Header().Get("Content-Disposition"), w.Body.String())
	}
	r := httptest.NewRequest("GET", "https://files.test:8443/docs/a.html", nil)
	r.SetBasicAuth("ai", "test-password")
	r.Header.Set("Sec-Fetch-Dest", "document")
	api := httptest.NewRecorder()
	g.ServeHTTP(api, r)
	if api.Body.String() != page {
		t.Fatal("API read was modified")
	}
}
