package main

import (
	"net/http/httptest"
	"testing"
)

func TestStaleBrowserCredentials(t *testing.T) {
	g, _ := setup(t)
	req := httptest.NewRequest("GET", "https://files.test:8443/", nil)
	req.SetBasicAuth("ai", "old-cached-password")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 303 {
		t.Fatal("cached native credentials must not block the login page")
	}
	cookie := login(t, g)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal("session must take precedence over cached native credentials")
	}
}
