package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

//go:embed login.html
var loginHTML string
var loginTemplate = template.Must(template.New("login").Parse(loginHTML))

const cookieName = "__Host-dufs_session"
const loginPath = "/__dufs/login"
const sessionTTL = 8 * time.Hour

type sessionAuthKey struct{}
type attempts struct {
	count   int
	expires time.Time
}
type gateway struct {
	origin      string
	host        string
	username    string
	password    string
	credentials [32]byte
	proxy       *httputil.ReverseProxy
	mu          sync.Mutex
	sessions    map[string]time.Time
	attempts    map[string]attempts
}

func newGateway(origin, upstream, username, password string) (*gateway, error) {
	public, err := url.Parse(origin)
	if err != nil || public.Scheme != "https" || public.Host == "" || public.User != nil || public.RawQuery != "" || public.Fragment != "" || (public.Path != "" && public.Path != "/") {
		return nil, fmt.Errorf("PUBLIC_ORIGIN must be an HTTPS origin")
	}
	target, err := url.Parse(upstream)
	if err != nil || target.Scheme != "http" || target.Host == "" || target.User != nil || target.RawQuery != "" || target.Fragment != "" || (target.Path != "" && target.Path != "/") {
		return nil, fmt.Errorf("UPSTREAM must be an internal HTTP origin")
	}
	if username == "" || strings.ContainsAny(username, ":\r\n") || password == "" {
		return nil, fmt.Errorf("invalid credentials configuration")
	}
	g := &gateway{origin: "https://" + public.Host, host: public.Host, username: username, password: password,
		credentials: sha256.Sum256([]byte(username + "\x00" + password)), sessions: make(map[string]time.Time), attempts: make(map[string]attempts)}
	g.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = target.Host
			pr.SetXForwarded()
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("Proxy-Authorization")
			if pr.In.Context().Value(sessionAuthKey{}) == true {
				pr.Out.SetBasicAuth(g.username, g.password)
			}
		},
		ModifyResponse: func(r *http.Response) error {
			// The browser must never see dufs's native authentication popup again.
			r.Header.Del("WWW-Authenticate")
			r.Header.Del("Set-Cookie")
			r.Header.Set("Cache-Control", "private, no-store")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "File service unavailable", http.StatusBadGateway)
		},
		Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ResponseHeaderTimeout: 60 * time.Second, IdleConnTimeout: 90 * time.Second, MaxIdleConns: 100, MaxIdleConnsPerHost: 20},
	}
	return g, nil
}

func (g *gateway) safeNext(value string) string {
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.ContainsAny(value, "\\\r\n") {
		return "/"
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || strings.HasPrefix(parsed.Path, "/__dufs/") || strings.HasPrefix(parsed.Path, "//") || strings.ContainsAny(parsed.Path, "\\\r\n") {
		return "/"
	}
	return parsed.RequestURI()
}

func (g *gateway) validSession(r *http.Request) bool {
	cookie, err := r.Cookie(cookieName)
	if err != nil || len(cookie.Value) != 43 {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	expires, found := g.sessions[cookie.Value]
	if !found {
		return false
	}
	if time.Now().After(expires) {
		delete(g.sessions, cookie.Value)
		return false
	}
	return true
}

func (g *gateway) allowAttempt(r *http.Request) bool {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	for key, value := range g.attempts {
		if now.After(value.expires) {
			delete(g.attempts, key)
		}
	}
	value, found := g.attempts[ip]
	if !found {
		if len(g.attempts) >= 1024 {
			return false
		}
		value.expires = now.Add(time.Minute)
	}
	if value.count >= 20 {
		return false
	}
	value.count++
	g.attempts[ip] = value
	return true
}

func (g *gateway) issueSession(w http.ResponseWriter, r *http.Request) bool {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		http.Error(w, "Login unavailable", 503)
		return false
	}
	value := base64.RawURLEncoding.EncodeToString(token)
	now := time.Now()
	g.mu.Lock()
	for key, expires := range g.sessions {
		if now.After(expires) {
			delete(g.sessions, key)
		}
	}
	if old, err := r.Cookie(cookieName); err == nil {
		delete(g.sessions, old.Value)
	}
	if len(g.sessions) >= 256 {
		g.mu.Unlock()
		http.Error(w, "Too many active sessions", 503)
		return false
	}
	g.sessions[value] = now.Add(sessionTTL)
	g.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: value, Path: "/", Secure: true, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: int(sessionTTL.Seconds()), Expires: now.Add(sessionTTL)})
	return true
}

func (g *gateway) page(w http.ResponseWriter, status int, next, username, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	w.WriteHeader(status)
	_ = loginTemplate.Execute(w, struct{ Next, Username, Error string }{next, username, message})
}

func redirectMarkdown(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != "GET" || r.Header.Get("Sec-Fetch-Mode") != "navigate" || r.Header.Get("Sec-Fetch-Dest") != "document" || !strings.EqualFold(path.Ext(r.URL.Path), ".md") {
		return false
	}
	query := r.URL.Query()
	for _, flag := range []string{"raw", "download", "json", "view", "edit", "hash", "tokengen"} {
		if query.Has(flag) {
			return false
		}
	}
	parent := path.Dir(r.URL.Path)
	if parent != "/" {
		parent += "/"
	}
	target := url.URL{Path: parent, RawQuery: url.Values{"q": {""}, "preview": {path.Base(r.URL.Path)}}.Encode()}
	http.Redirect(w, r, target.String(), http.StatusSeeOther)
	return true
}

func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Host != g.host {
		http.Error(w, "Unexpected host", 400)
		return
	}
	if r.URL.Path == loginPath {
		next := g.safeNext(r.URL.Query().Get("next"))
		if r.Method == "GET" {
			if g.validSession(r) {
				http.Redirect(w, r, next, http.StatusSeeOther)
				return
			}
			g.page(w, 200, next, g.username, "")
			return
		}
		if r.Method != "POST" {
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "Method not allowed", 405)
			return
		}
		if r.Header.Get("Origin") != g.origin {
			http.Error(w, "Invalid origin", 403)
			return
		}
		if !g.allowAttempt(r) {
			w.Header().Set("Retry-After", "60")
			g.page(w, 429, next, g.username, "Too many attempts. Please try again in one minute.")
			return
		}
		if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/x-www-form-urlencoded" {
			http.Error(w, "Expected a login form", 415)
			return
		}
		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(15 * time.Second))
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid or oversized form", 400)
			return
		}
		next = g.safeNext(r.PostForm.Get("next"))
		username, password := r.PostForm.Get("username"), r.PostForm.Get("password")
		actual := sha256.Sum256([]byte(username + "\x00" + password))
		if subtle.ConstantTimeCompare(actual[:], g.credentials[:]) != 1 {
			g.page(w, 401, next, username, "Incorrect username or password. Please try again.")
			return
		}
		if g.issueSession(w, r) {
			http.Redirect(w, r, next, http.StatusSeeOther)
		}
		return
	}
	if r.URL.Path == "/__dufs/logout" || r.Method == "LOGOUT" {
		if r.Method != "POST" && r.Method != "LOGOUT" {
			w.Header().Set("Allow", "POST")
			http.Error(w, "Method not allowed", 405)
			return
		}
		if r.Header.Get("Origin") != g.origin {
			http.Error(w, "Invalid origin", 403)
			return
		}
		if cookie, err := r.Cookie(cookieName); err == nil {
			g.mu.Lock()
			delete(g.sessions, cookie.Value)
			g.mu.Unlock()
		}
		http.SetCookie(w, &http.Cookie{Name: cookieName, Path: "/", Value: "", MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		if r.Method == "LOGOUT" {
			w.WriteHeader(204)
		} else {
			http.Redirect(w, r, loginPath, http.StatusSeeOther)
		}
		return
	}
	if strings.HasPrefix(r.URL.Path, "/__dufs/") {
		http.NotFound(w, r)
		return
	}
	// A session takes precedence over stale HTTP credentials cached by Chrome.
	if g.validSession(r) {
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" && r.Header.Get("Origin") != g.origin {
			http.Error(w, "Invalid origin", 403)
			return
		}
		if redirectMarkdown(w, r) {
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), sessionAuthKey{}, true))
		g.proxy.ServeHTTP(w, r)
		return
	}
	if r.Method == "GET" && (r.Header.Get("Sec-Fetch-Mode") == "navigate" || (r.Header.Get("Authorization") == "" && strings.Contains(r.Header.Get("Accept"), "text/html"))) {
		http.Redirect(w, r, loginPath+"?next="+url.QueryEscape(g.safeNext(r.URL.RequestURI())), http.StatusSeeOther)
		return
	}
	// Explicit Authorization is for AI/WebDAV clients; dufs validates it as before.
	if r.Header.Get("Authorization") != "" {
		g.proxy.ServeHTTP(w, r)
		return
	}
	http.Error(w, "Login required", http.StatusUnauthorized)
}

func main() {
	password, err := os.ReadFile(os.Getenv("DUFS_PASSWORD_FILE"))
	if err != nil {
		log.Fatal("Cannot read DUFS_PASSWORD_FILE")
	}
	g, err := newGateway(os.Getenv("PUBLIC_ORIGIN"), os.Getenv("UPSTREAM"), os.Getenv("DUFS_USERNAME"), strings.TrimSpace(string(password)))
	if err != nil {
		log.Fatal(err)
	}
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	server := &http.Server{Addr: addr, Handler: g, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	log.Print("dufs login gateway listening")
	log.Fatal(server.ListenAndServe())
}
