package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed login.html
var loginHTML string
var loginTemplate = template.Must(template.New("login").Parse(loginHTML))

const cookieName = "__Host-dufs_session"
const loginPath = "/__dufs/login"
const sessionTTL = 90 * 24 * time.Hour

type sessionAuthKey struct{}
type downloadKey struct{}
type buttonKey struct{}

const maxButtonPage = 10 << 20

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
	upstream    *url.URL
	uploads     sync.Mutex
	mu          sync.Mutex
	sessionKey  []byte
	revoked     map[string]time.Time
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
	g := &gateway{origin: "https://" + public.Host, host: public.Host, username: username, password: password, upstream: target,
		credentials: sha256.Sum256([]byte(username + "\x00" + password)), revoked: make(map[string]time.Time), attempts: make(map[string]attempts)}
	// Signed, stateless sessions survive restarts; rotating the password revokes them all.
	key := hmac.New(sha256.New, []byte(password))
	key.Write([]byte("pi-dufs-session-v1\x00" + username))
	g.sessionKey = key.Sum(nil)
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
			if name, ok := r.Request.Context().Value(downloadKey{}).(string); ok && r.StatusCode == http.StatusOK {
				r.Header.Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
			}
			if href, ok := r.Request.Context().Value(buttonKey{}).(string); ok {
				return addDownloadButton(r, href)
			}
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

func (g *gateway) signSession(expires time.Time, nonce []byte) string {
	payload := binary.BigEndian.AppendUint64(nil, uint64(expires.Unix()))
	payload = append(payload, nonce...)
	mac := hmac.New(sha256.New, g.sessionKey)
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (g *gateway) validSession(r *http.Request) bool {
	cookie, err := r.Cookie(cookieName)
	if err != nil || len(cookie.Value) > 128 {
		return false
	}
	encoded, signature, ok := strings.Cut(cookie.Value, ".")
	payload, err1 := base64.RawURLEncoding.DecodeString(encoded)
	actual, err2 := base64.RawURLEncoding.DecodeString(signature)
	if !ok || err1 != nil || err2 != nil || len(payload) != 24 {
		return false
	}
	mac := hmac.New(sha256.New, g.sessionKey)
	mac.Write(payload)
	if !hmac.Equal(actual, mac.Sum(nil)) || time.Now().Unix() >= int64(binary.BigEndian.Uint64(payload)) {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	_, revoked := g.revoked[cookie.Value]
	return !revoked
}

// shortcut: logout revocation is in memory, so a copied cookie works again after a gateway restart until expiry; rotate the password to revoke everything.
func (g *gateway) revoke(r *http.Request) {
	cookie, err := r.Cookie(cookieName)
	if err != nil || !g.validSession(r) {
		return
	}
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	for value, expires := range g.revoked {
		if now.After(expires) {
			delete(g.revoked, value)
		}
	}
	if len(g.revoked) < 4096 {
		g.revoked[cookie.Value] = now.Add(sessionTTL)
	}
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
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		http.Error(w, "Login unavailable", 503)
		return false
	}
	g.revoke(r)
	expires := time.Now().Add(sessionTTL)
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: g.signSession(expires, nonce), Path: "/", Secure: true, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: int(sessionTTL.Seconds()), Expires: expires})
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

// Serialize PUTs so conditional creates cannot race, including empty files.
func (g *gateway) serveFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut {
		g.uploads.Lock()
		defer g.uploads.Unlock()
		if r.Header.Get("If-None-Match") == "*" {
			head := r.Clone(r.Context())
			head.Method, head.Body, head.GetBody, head.ContentLength = "HEAD", nil, nil, 0
			head.RequestURI = ""
			head.URL = &url.URL{Scheme: g.upstream.Scheme, Host: g.upstream.Host, Path: r.URL.Path, RawPath: r.URL.RawPath}
			head.Host = g.upstream.Host
			head.Header = make(http.Header)
			head.Header.Set("Authorization", r.Header.Get("Authorization"))
			head.TransferEncoding, head.Trailer = nil, nil
			if r.Context().Value(sessionAuthKey{}) == true {
				head.SetBasicAuth(g.username, g.password)
			}
			response, err := g.proxy.Transport.RoundTrip(head)
			if err != nil {
				http.Error(w, "File service unavailable", 502)
				return
			}
			response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				http.Error(w, "Destination already exists", http.StatusPreconditionFailed)
				return
			}
			if response.StatusCode != http.StatusNotFound {
				status := response.StatusCode
				if status < 400 {
					status = 502
				}
				http.Error(w, "Cannot check destination", status)
				return
			}
		}
	}
	g.proxy.ServeHTTP(w, r)
}

// Browser page views of user HTML get a floating link to the original bytes.
func withDownloadControls(r *http.Request) *http.Request {
	if r.Method != http.MethodGet {
		return r
	}
	query := r.URL.Query()
	base := path.Base(r.URL.Path)
	isDir := strings.HasSuffix(r.URL.Path, "/")
	if query.Has("download") {
		if isDir {
			base = "index.html"
		}
		return r.WithContext(context.WithValue(r.Context(), downloadKey{}, base))
	}
	ext := strings.ToLower(path.Ext(r.URL.Path))
	if len(query) != 0 || r.Header.Get("Sec-Fetch-Dest") != "document" || !(isDir || ext == ".html" || ext == ".htm") {
		return r
	}
	href := url.PathEscape(base) + "?download"
	if isDir {
		href = "index.html?download"
	}
	return r.WithContext(context.WithValue(r.Context(), buttonKey{}, href))
}

func addDownloadButton(r *http.Response, href string) error {
	if r.StatusCode != http.StatusOK || r.Header.Get("Content-Encoding") != "" || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/html") || r.ContentLength > maxButtonPage {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxButtonPage+1))
	r.Body.Close()
	if err != nil {
		return err
	}
	// Skip oversized pages and the file-manager UI itself (directory listings).
	if len(body) <= maxButtonPage && !bytes.Contains(body, []byte("__DUFS_PREFIX__")) {
		button := []byte(`<a href="` + href + `" download aria-label="Download original file" style="position:fixed;right:16px;bottom:16px;z-index:2147483647;padding:10px 16px;border-radius:999px;background:#4169c1;color:#fff;font:600 14px/1.2 system-ui,sans-serif;text-decoration:none;box-shadow:0 2px 8px rgba(0,0,0,.35)">&#11015; Download</a>`)
		if i := bytes.LastIndex(bytes.ToLower(body), []byte("</body>")); i >= 0 {
			body = append(body[:i:i], append(button, body[i:]...)...)
		} else {
			body = append(body, button...)
		}
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Set("Content-Length", strconv.Itoa(len(body)))
	r.Header.Del("ETag")
	r.Header.Del("Last-Modified")
	return nil
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
		g.revoke(r)
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
		g.serveFiles(w, withDownloadControls(r))
		return
	}
	if r.Method == "GET" && (r.Header.Get("Sec-Fetch-Mode") == "navigate" || (r.Header.Get("Authorization") == "" && strings.Contains(r.Header.Get("Accept"), "text/html"))) {
		http.Redirect(w, r, loginPath+"?next="+url.QueryEscape(g.safeNext(r.URL.RequestURI())), http.StatusSeeOther)
		return
	}
	// Explicit Authorization is for AI/WebDAV clients; dufs validates it as before.
	if r.Header.Get("Authorization") != "" {
		g.serveFiles(w, r)
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
