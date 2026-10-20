package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func setup(t *testing.T) (*gateway, *httptest.Server) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "ai" || pass != "test-password" {
			w.Header().Set("WWW-Authenticate", `Basic realm="DUFS"`)
			http.Error(w, "denied", 401)
			return
		}
		if r.Header.Get("Cookie") != "" {
			t.Error("session cookie leaked to backend")
		}
		if r.Header.Get("Range") == "bytes=0-2" {
			w.Header().Set("Content-Range", "bytes 0-2/6")
			w.WriteHeader(206)
			_, _ = io.WriteString(w, "abc")
			return
		}
		w.Header().Set("X-Test-Method", r.Method)
		w.Header().Set("Cache-Control", "public, max-age=1000")
		if r.Method == "PUT" {
			_, _ = io.Copy(w, r.Body)
			return
		}
		_, _ = io.WriteString(w, "file-content")
	}))
	t.Cleanup(upstream.Close)
	g, err := newGateway("https://files.test:8443", upstream.URL, "ai", "test-password")
	if err != nil {
		t.Fatal(err)
	}
	return g, upstream
}

func call(g *gateway, method, path, body, origin string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "https://files.test:8443"+path, strings.NewReader(body))
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if method == "POST" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	return w
}

func login(t *testing.T, g *gateway) *http.Cookie {
	t.Helper()
	w := call(g, "POST", loginPath, "username=ai&password=test-password&next=%2Fdemo%2F", g.origin, nil)
	if w.Code != 303 || w.Header().Get("Location") != "/demo/" {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing cookie")
	}
	c := cookies[0]
	if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.MaxAge != 28800 {
		t.Fatal("unsafe cookie attributes")
	}
	return c
}

func TestPageAndAuthentication(t *testing.T) {
	g, _ := setup(t)
	w := call(g, "GET", loginPath, "", "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `autocomplete="current-password"`) || !strings.Contains(w.Body.String(), `value="ai"`) {
		t.Fatal("missing usable login form")
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("missing login CSP")
	}
	req := httptest.NewRequest("GET", "https://files.test:8443/demo/?x=1", nil)
	req.Header.Set("Accept", "text/html")
	w = httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 303 || !strings.HasPrefix(w.Header().Get("Location"), loginPath+"?next=") {
		t.Fatal("navigation must go to login")
	}
	w = call(g, "GET", "/demo.js", "", "", nil)
	if w.Code != 401 || w.Header().Get("WWW-Authenticate") != "" {
		t.Fatal("must not trigger native auth popup")
	}
	w = call(g, "POST", loginPath, "username=ai&password=WRONG", g.origin, nil)
	if w.Code != 401 || strings.Contains(w.Body.String(), "WRONG") || len(w.Result().Cookies()) != 0 {
		t.Fatal("bad password accepted or echoed")
	}
	cookie := login(t, g)
	w = call(g, "GET", "/demo/index.html", "", "", cookie)
	if w.Code != 200 || w.Body.String() != "file-content" {
		t.Fatal("authenticated preview failed")
	}
	if w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("private response is cacheable")
	}
	for _, method := range []string{"CHECKAUTH", "PROPFIND", "MKCOL"} {
		w = call(g, method, "/", "", g.origin, cookie)
		if w.Code != 200 || w.Header().Get("X-Test-Method") != method {
			t.Fatalf("method %s broken", method)
		}
	}
}

func TestCSRFLogoutAndExpiry(t *testing.T) {
	g, _ := setup(t)
	for _, origin := range []string{"", "https://evil.test"} {
		if call(g, "POST", loginPath, "username=ai&password=test-password", origin, nil).Code != 403 {
			t.Fatal("login CSRF accepted")
		}
	}
	cookie := login(t, g)
	if call(g, "PUT", "/file", "attack", "https://evil.test", cookie).Code != 403 {
		t.Fatal("write CSRF accepted")
	}
	if call(g, "POST", "/__dufs/logout", "", "https://evil.test", cookie).Code != 403 {
		t.Fatal("logout CSRF accepted")
	}
	w := call(g, "LOGOUT", "/", "", g.origin, cookie)
	if w.Code != 204 || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("dufs native logout not handled")
	}
	if call(g, "GET", "/file", "", "", cookie).Code != 401 {
		t.Fatal("logged-out session still accepted")
	}
	cookie = login(t, g)
	g.mu.Lock()
	g.sessions[cookie.Value] = time.Now().Add(-time.Second)
	g.mu.Unlock()
	if call(g, "GET", "/file", "", "", cookie).Code != 401 {
		t.Fatal("expired session accepted")
	}
}

func TestAPIStreamingAndRanges(t *testing.T) {
	g, _ := setup(t)
	payload := strings.Repeat("large-upload", 100000)
	req := httptest.NewRequest("PUT", "https://files.test:8443/new/file", strings.NewReader(payload))
	req.SetBasicAuth("ai", "test-password")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 200 || w.Body.String() != payload {
		t.Fatal("AI streaming upload broken")
	}
	req = httptest.NewRequest("GET", "https://files.test:8443/file", nil)
	req.SetBasicAuth("ai", "wrong")
	w = httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 401 || w.Header().Get("WWW-Authenticate") != "" {
		t.Fatal("bad API auth accepted or native popup triggered")
	}
	cookie := login(t, g)
	req = httptest.NewRequest("GET", "https://files.test:8443/file", nil)
	req.AddCookie(cookie)
	req.Header.Set("Range", "bytes=0-2")
	w = httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 206 || w.Body.String() != "abc" || w.Header().Get("Content-Range") == "" {
		t.Fatal("range request broken")
	}
}

func TestBoundaries(t *testing.T) {
	g, _ := setup(t)
	for _, value := range []string{"https://evil.test", "//evil.test", "/\\evil.test", "/%2f%2fevil.test", "/%5cevil.test", "/__dufs/login", "/%0d%0aLocation:evil"} {
		if g.safeNext(value) != "/" {
			t.Fatalf("unsafe redirect %q", value)
		}
	}
	if g.safeNext("/demo/?x=1") != "/demo/?x=1" {
		t.Fatal("legitimate return URL lost")
	}
	w := call(g, "POST", loginPath, url.Values{"username": {`"><script>alert(1)</script>`}, "password": {"wrong"}}.Encode(), g.origin, nil)
	if strings.Contains(w.Body.String(), "<script>") {
		t.Fatal("username injection")
	}
	w = call(g, "POST", loginPath, "password="+strings.Repeat("x", 5000), g.origin, nil)
	if w.Code != 400 {
		t.Fatal("oversized login accepted")
	}
	req := httptest.NewRequest("GET", "https://evil.test/", nil)
	w = httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatal("unexpected host accepted")
	}
	for i := 0; i < 20; i++ {
		call(g, "POST", loginPath, "username=ai&password=wrong", g.origin, nil)
	}
	if call(g, "POST", loginPath, "username=ai&password=wrong", g.origin, nil).Code != 429 {
		t.Fatal("rate limit missing")
	}
}

func TestUpstreamUnavailable(t *testing.T) {
	g, upstream := setup(t)
	cookie := login(t, g)
	upstream.Close()
	if call(g, "GET", "/", "", "", cookie).Code != 502 {
		t.Fatal("upstream failure not reported")
	}
}
