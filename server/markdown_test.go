package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestMarkdownDirectNavigation(t *testing.T) {
	g, _ := setup(t)
	cookie := login(t, g)
	for _, file := range []string{"/folder/report.md", "/文档/文件 #1.MD", "/root.md"} {
		address := url.URL{Scheme: "https", Host: g.host, Path: file}
		r := httptest.NewRequest("GET", address.String(), nil)
		r.AddCookie(cookie)
		r.Header.Set("Sec-Fetch-Mode", "navigate")
		r.Header.Set("Sec-Fetch-Dest", "document")
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != http.StatusSeeOther {
			t.Fatalf("%s: %d", file, w.Code)
		}
		target, err := url.Parse(w.Header().Get("Location"))
		if err != nil || target.IsAbs() || target.Host != "" || target.Query().Get("preview") == "" || !target.Query().Has("q") {
			t.Fatalf("unsafe/missing preview redirect: %v", target)
		}
		if file == "/root.md" && target.Path != "/" {
			t.Fatal("root parent is incorrect")
		}
		if file == "/文档/文件 #1.MD" && (target.Path != "/文档/" || target.Query().Get("preview") != "文件 #1.MD") {
			t.Fatal("Unicode filename lost")
		}
	}
	for _, suffix := range []string{"?raw=1", "?download", "?json", "?view", "?edit", "?hash", "?tokengen"} {
		r := httptest.NewRequest("GET", "https://"+g.host+"/file.md"+suffix, nil)
		r.AddCookie(cookie)
		r.Header.Set("Sec-Fetch-Mode", "navigate")
		r.Header.Set("Sec-Fetch-Dest", "document")
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != 200 || w.Body.String() != "file-content" {
			t.Fatalf("raw operation changed: %s", suffix)
		}
	}
	for _, dest := range []string{"", "empty"} {
		r := httptest.NewRequest("GET", "https://"+g.host+"/file.md", nil)
		r.AddCookie(cookie)
		r.Header.Set("Sec-Fetch-Mode", "navigate")
		r.Header.Set("Sec-Fetch-Dest", dest)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal("non-document request redirected")
		}
	}
	r := httptest.NewRequest("GET", "https://"+g.host+"/file.md", nil)
	r.SetBasicAuth("ai", "test-password")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != "file-content" {
		t.Fatal("AI raw file access changed")
	}
	r = httptest.NewRequest("GET", "https://"+g.host+"/index.html", nil)
	r.AddCookie(cookie)
	r.Header.Set("Sec-Fetch-Mode", "navigate")
	r.Header.Set("Sec-Fetch-Dest", "document")
	w = httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("HTML navigation changed")
	}
}
