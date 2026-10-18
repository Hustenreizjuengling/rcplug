package host

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPBKDF2Vectors(t *testing.T) {
	for iter, want := range map[int]string{ // RFC 7914, section 11
		1:    "120fb6cffcf8b32c43e7225256c4f837a86548c92ccc35480805987cb70be17b",
		2:    "ae4d0c95af6b46d32d0adff928f06dd02a303f8ef3c251dfd6e2d85a95474c43",
		4096: "c5e478d59288c841aa530db6845c4c8d962893a001ce4e11a4963873aa98134a",
	} {
		if got := hex.EncodeToString(pbkdf2([]byte("password"), []byte("salt"), iter)); got != want {
			t.Errorf("%d iterations: %s", iter, got)
		}
	}
}

type accessRig struct {
	t    *testing.T
	root string
	srv  *Server
	h    http.Handler
}

func newAccessRig(t *testing.T) *accessRig {
	old := pbkdfIter
	pbkdfIter = 1000
	t.Cleanup(func() { pbkdfIter = old })
	root := t.TempDir()
	m := NewManager(root, 19500)
	if _, err := m.Load(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.StopAll)
	srv := NewServer(m, nil, "test")
	srv.Access = NewAccess(root)
	return &accessRig{t: t, root: root, srv: srv, h: srv.Handler()}
}

// do sends a request from a computer on the network (or from the device
// itself with remote 127.0.0.1).
func (r *accessRig) do(method, path, body, remote, cookie string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://192.168.1.20:8080"+path, strings.NewReader(body))
	req.RemoteAddr = remote
	req.Header.Set("X-Rcplug", "1")
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)
	return rec
}

const lan = "192.168.1.30:50000"

func sessionOf(rec *httptest.ResponseRecorder) string {
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			return c.Value
		}
	}
	return ""
}

func TestDashboardPassword(t *testing.T) {
	r := newAccessRig(t)
	if rec := r.do("GET", "/api/plugins", "", lan, ""); rec.Code != 200 {
		t.Fatalf("open without a password: %d", rec.Code)
	}
	if rec := r.do("POST", "/api/access/password", `{"new":"short"}`, lan, ""); rec.Code != 400 {
		t.Fatalf("too short a password: %d %s", rec.Code, rec.Body)
	}
	rec := r.do("POST", "/api/access/password", `{"new":"studio-secret"}`, lan, "")
	if rec.Code != 200 || sessionOf(rec) == "" {
		t.Fatalf("set: %d %s", rec.Code, rec.Body)
	}
	if fi, err := os.Stat(filepath.Join(r.root, "config", accessFile)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("password file: %v %v", fi, err)
	}
	raw, _ := os.ReadFile(filepath.Join(r.root, "config", accessFile))
	if strings.Contains(string(raw), "studio-secret") {
		t.Fatal("password stored in clear")
	}

	// Without a session: the API says so, a page leads to the login.
	if rec := r.do("GET", "/api/plugins", "", lan, ""); rec.Code != 401 || !strings.Contains(rec.Body.String(), "login_required") {
		t.Fatalf("API without login: %d %s", rec.Code, rec.Body)
	}
	if rec := r.do("GET", "/plugins/console/", "", lan, "", "Accept", "text/html"); rec.Code != 303 || rec.Header().Get("Location") != "/login" {
		t.Fatalf("page without login: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	for _, p := range []string{"/login", "/api/access"} {
		if rec := r.do("GET", p, "", lan, ""); rec.Code != 200 {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
	// The device itself (a plugin) is not asked; a proxied request is.
	if rec := r.do("GET", "/api/plugins", "", "127.0.0.1:40000", ""); rec.Code != 200 {
		t.Fatalf("from the device: %d", rec.Code)
	}
	if rec := r.do("GET", "/api/plugins", "", "127.0.0.1:40000", "", "X-Forwarded-For", "192.168.1.30"); rec.Code != 401 {
		t.Fatalf("forwarded from the network: %d", rec.Code)
	}

	// Log in.
	if rec := r.do("POST", "/api/login", `{"password":"wrong-one"}`, lan, ""); rec.Code != 401 || !strings.Contains(rec.Body.String(), "wrong_password") {
		t.Fatalf("wrong password: %d %s", rec.Code, rec.Body)
	}
	rec = r.do("POST", "/api/login", `{"password":"studio-secret"}`, lan, "")
	s1 := sessionOf(rec)
	if rec.Code != 200 || s1 == "" || !strings.Contains(rec.Header().Get("Set-Cookie"), "HttpOnly") || !strings.Contains(rec.Header().Get("Set-Cookie"), "SameSite=Strict") {
		t.Fatalf("login: %d %v", rec.Code, rec.Header())
	}
	if rec := r.do("GET", "/api/plugins", "", lan, s1); rec.Code != 200 {
		t.Fatalf("with a session: %d", rec.Code)
	}
	if rec := r.do("GET", "/api/plugins", "", lan, s1[:len(s1)-2]+"xx"); rec.Code != 401 {
		t.Fatalf("forged session: %d", rec.Code)
	}
	if st := r.do("GET", "/api/access", "", lan, s1).Body.String(); !strings.Contains(st, `"loggedIn":true`) || !strings.Contains(st, `"protected":true`) {
		t.Fatalf("status: %s", st)
	}

	// A new password ends the other sessions; the changing browser stays in.
	if rec := r.do("POST", "/api/access/password", `{"current":"bad","new":"another-secret"}`, lan, s1); rec.Code != 401 {
		t.Fatalf("change with a wrong current password: %d", rec.Code)
	}
	rec = r.do("POST", "/api/access/password", `{"current":"studio-secret","new":"another-secret"}`, lan, s1)
	s2 := sessionOf(rec)
	if rec.Code != 200 || s2 == "" {
		t.Fatalf("change: %d %s", rec.Code, rec.Body)
	}
	if rec := r.do("GET", "/api/plugins", "", lan, s1); rec.Code != 401 {
		t.Fatalf("old session after a change: %d", rec.Code)
	}
	if rec := r.do("GET", "/api/plugins", "", lan, s2); rec.Code != 200 {
		t.Fatalf("new session: %d", rec.Code)
	}

	// Guessing locks the login for a minute.
	for i := 0; i < loginFailures; i++ {
		r.do("POST", "/api/login", `{"password":"guess-`+string(rune('a'+i))+`"}`, lan, "")
	}
	if rec := r.do("POST", "/api/login", `{"password":"another-secret"}`, lan, ""); rec.Code != 429 {
		t.Fatalf("after guessing: %d %s", rec.Code, rec.Body)
	}

	// Removing the file (the toolkit's way) opens the dashboard at once.
	if err := os.Remove(filepath.Join(r.root, "config", accessFile)); err != nil {
		t.Fatal(err)
	}
	if rec := r.do("GET", "/api/plugins", "", lan, ""); rec.Code != 200 {
		t.Fatalf("after removing the file: %d", rec.Code)
	}
}

func TestDashboardPasswordResetAtTheDevice(t *testing.T) {
	r := newAccessRig(t)
	if rec := r.do("POST", "/api/access/reset", "", lan, ""); rec.Code != 409 {
		t.Fatalf("reset without a password: %d", rec.Code)
	}
	if rec := r.do("POST", "/api/access/password", `{"new":"studio-secret"}`, lan, ""); rec.Code != 200 {
		t.Fatalf("set: %d", rec.Code)
	}
	if rec := r.do("POST", "/api/access/reset", "", lan, ""); rec.Code != 409 || !strings.Contains(rec.Body.String(), "no_presence") {
		t.Fatalf("reset without the console: %d %s", rec.Code, rec.Body)
	}
	// The presses do not come: nothing changes.
	r.srv.Access.Presence = func(ctx context.Context) error { return errors.New("time is up; nothing was changed") }
	if rec := r.do("POST", "/api/access/reset", "", lan, ""); rec.Code != 200 {
		t.Fatalf("start reset: %d %s", rec.Code, rec.Body)
	}
	waitFor(t, "reset ends", 3*time.Second, func() bool {
		return !strings.Contains(r.do("GET", "/api/access", "", lan, "").Body.String(), `"active":true`)
	})
	if st := r.do("GET", "/api/access", "", lan, "").Body.String(); !strings.Contains(st, `"protected":true`) || !strings.Contains(st, "time is up") {
		t.Fatalf("after a failed reset: %s", st)
	}
	// The presses come.
	r.srv.Access.Presence = func(ctx context.Context) error { return nil }
	if rec := r.do("POST", "/api/access/reset", "", lan, ""); rec.Code != 200 {
		t.Fatalf("start reset: %d", rec.Code)
	}
	waitFor(t, "password removed", 3*time.Second, func() bool { return !r.srv.Access.Protected() })
	if rec := r.do("GET", "/api/plugins", "", lan, ""); rec.Code != 200 {
		t.Fatalf("open again: %d", rec.Code)
	}
}
