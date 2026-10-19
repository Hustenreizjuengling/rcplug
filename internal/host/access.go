package host

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Hustenreizjuengling/rcplug/sdk"
)

// Dashboard access (optional): with a password set, every page and API of
// the host, the plugins' pages included, needs a login. Requests from the
// device itself (plugins calling each other over the loopback interface)
// stay free. Off by default, as everyone on a trusted home or studio network
// was meant to use the device.
//
// The password is kept as a PBKDF2-SHA256 hash in config/dashboard-access.json
// (root only), together with a random key that signs the session cookies, so
// a login survives restarts and updates of rcplug; a new password logs
// everybody out. A forgotten password is removed at the device: four presses
// of channel 1's Listen button, which the console reports (see Presence).

const (
	accessFile    = "dashboard-access.json"
	sessionCookie = "rcplug_session"
	sessionLife   = 30 * 24 * time.Hour
	minPassword   = 6
	maxPassword   = 128
	loginFailures = 5
	loginWindow   = 5 * time.Minute
	loginLockout  = time.Minute
)

// pbkdfIter: about a fifth of a second per check on the device (tests lower it).
var pbkdfIter = 100000

var (
	errLogin       = sdk.NewError(http.StatusUnauthorized, "login_required", "log in to the dashboard first")
	errWrongPass   = sdk.NewError(http.StatusUnauthorized, "wrong_password", "wrong password")
	errLoginLocked = sdk.NewError(http.StatusTooManyRequests, "login_locked", "too many wrong passwords; try again in a minute")
)

type accessData struct {
	Hash string `json:"hash"` // pbkdf2-sha256$<iterations>$<salt>$<hash>, base64
	Key  string `json:"key"`  // base64, signs the session cookies
}

// Access guards the dashboard with an optional password.
type Access struct {
	path string
	// Presence waits until someone presses channel 1's Listen button four
	// times at the device (the console reports it); nil: not available.
	Presence func(ctx context.Context) error

	mu       sync.Mutex
	data     *accessData // nil: no password
	modTime  time.Time   // of the file when it was read
	failures []time.Time
	locked   time.Time
	reset    resetState
}

type resetState struct {
	active bool
	until  time.Time
	done   time.Time
	err    string
}

// NewAccess reads the password file in <root>/config.
func NewAccess(root string) *Access {
	return &Access{path: filepath.Join(root, "config", accessFile)}
}

// refreshLocked reads the file again if it changed, so removing it (with
// the toolkit, or by hand) takes effect at once.
func (a *Access) refreshLocked() {
	fi, err := os.Stat(a.path)
	if err != nil {
		a.data, a.modTime = nil, time.Time{}
		return
	}
	if a.data != nil && fi.ModTime().Equal(a.modTime) {
		return
	}
	var d accessData
	if err := sdk.LoadJSON(a.path, &d); err != nil || d.Hash == "" || d.Key == "" {
		a.data = nil
		return
	}
	a.data, a.modTime = &d, fi.ModTime()
}

// Protected reports whether a password is set.
func (a *Access) Protected() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refreshLocked()
	return a.data != nil
}

// pbkdf2 derives 32 bytes with HMAC-SHA256 (RFC 8018, one block).
func pbkdf2(password, salt []byte, iter int) []byte {
	mac := hmac.New(sha256.New, password)
	mac.Write(salt)
	mac.Write([]byte{0, 0, 0, 1})
	u := mac.Sum(nil)
	out := append([]byte(nil), u...)
	for i := 1; i < iter; i++ {
		mac.Reset()
		mac.Write(u)
		u = mac.Sum(u[:0])
		for j := range out {
			out[j] ^= u[j]
		}
	}
	return out
}

func hashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	b64 := base64.RawStdEncoding.EncodeToString
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", pbkdfIter, b64(salt), b64(pbkdf2([]byte(pw), salt, pbkdfIter))), nil
}

func checkPassword(hash, pw string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1 || iter > 10000000 {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[2])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[3])
	if err1 != nil || err2 != nil {
		return false
	}
	return subtle.ConstantTimeCompare(pbkdf2([]byte(pw), salt, iter), want) == 1
}

// validPassword: printable characters (browsers send them as UTF-8 in JSON),
// long enough to resist guessing over the network for a while.
func validPassword(pw string) error {
	if len(pw) < minPassword || len(pw) > maxPassword || !printable(pw) {
		return sdk.NewError(http.StatusBadRequest, "invalid_password", fmt.Sprintf("the password needs %d to %d printable characters", minPassword, maxPassword))
	}
	return nil
}

// verifyLocked checks a password with throttling; it unlocks a.mu while
// hashing.
func (a *Access) verifyLocked(pw string) error {
	now := time.Now()
	if now.Before(a.locked) {
		return errLoginLocked
	}
	hash := a.data.Hash
	a.mu.Unlock()
	ok := checkPassword(hash, pw)
	a.mu.Lock()
	if ok {
		return nil
	}
	keep := a.failures[:0]
	for _, t := range a.failures {
		if now.Sub(t) < loginWindow {
			keep = append(keep, t)
		}
	}
	a.failures = append(keep, now)
	if len(a.failures) >= loginFailures {
		a.locked, a.failures = now.Add(loginLockout), nil
		return errLoginLocked
	}
	return errWrongPass
}

// SetPassword sets, changes (current must match) or removes (next == "")
// the password.
func (a *Access) SetPassword(current, next string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refreshLocked()
	if a.data != nil {
		if err := a.verifyLocked(current); err != nil {
			return err
		}
	}
	if next == "" {
		if err := os.Remove(a.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		a.data, a.modTime = nil, time.Time{}
		return nil
	}
	if err := validPassword(next); err != nil {
		return err
	}
	hash, err := hashPassword(next)
	if err != nil {
		return err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	d := &accessData{Hash: hash, Key: base64.RawStdEncoding.EncodeToString(key)}
	raw, _ := json.MarshalIndent(d, "", "  ")
	if err := os.MkdirAll(filepath.Dir(a.path), 0o755); err != nil {
		return err
	}
	if err := sdk.WriteFileAtomic(a.path, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	a.data = d
	if fi, err := os.Stat(a.path); err == nil {
		a.modTime = fi.ModTime()
	}
	return nil
}

// Login checks the password and returns a session cookie value.
func (a *Access) Login(pw string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refreshLocked()
	if a.data == nil {
		return "", sdk.NewError(http.StatusConflict, "no_password", "the dashboard has no password")
	}
	if err := a.verifyLocked(pw); err != nil {
		return "", err
	}
	if a.data == nil { // removed while hashing
		return "", sdk.NewError(http.StatusConflict, "no_password", "the dashboard has no password")
	}
	return a.signLocked(time.Now().Add(sessionLife)), nil
}

func (a *Access) signLocked(exp time.Time) string {
	key, _ := base64.RawStdEncoding.DecodeString(a.data.Key)
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(exp.Unix()))
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("rcplug-session:"))
	mac.Write(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:]) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// session reports whether a cookie value is a valid, unexpired session.
func (a *Access) session(v string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refreshLocked()
	if a.data == nil {
		return false
	}
	exp, _, ok := strings.Cut(v, ".")
	if !ok {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(exp)
	if err != nil || len(raw) != 8 {
		return false
	}
	t := time.Unix(int64(binary.BigEndian.Uint64(raw)), 0)
	if time.Now().After(t) {
		return false
	}
	return hmac.Equal([]byte(a.signLocked(t)), []byte(v))
}

// LoggedIn reports whether the request carries a valid session.
func (a *Access) LoggedIn(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	return err == nil && a.session(c.Value)
}

// fromDevice: a request from the device itself (a plugin), not proxied.
func fromDevice(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback() && r.Header.Get("X-Forwarded-For") == ""
}

// open lists what a visitor who is not logged in may reach.
func open(r *http.Request) bool {
	switch r.URL.Path {
	case "/login", "/api/access", "/api/login", "/api/logout", "/api/access/reset", "/favicon.ico",
		"/ui/rcplug.css", "/ui/icons.svg": // the login page's look
		return true
	}
	return false
}

// Gate lets requests through when no password is set, the visitor is logged
// in, or the request comes from the device itself; others go to the login.
func (a *Access) Gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.Protected() || open(r) || fromDevice(r) || a.LoggedIn(r) {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodGet && strings.Contains(r.Header.Get("Accept"), "text/html") {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		sdk.WriteError(w, errLogin)
	})
}

// setSession sets the session cookie; Secure only over HTTPS (in front of
// the host), as the device itself speaks plain HTTP.
func setSession(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"})
}

// StartReset opens a minute in which presses at the device remove the
// password (see Presence).
func (a *Access) StartReset() error {
	a.mu.Lock()
	a.refreshLocked()
	switch {
	case a.data == nil:
		a.mu.Unlock()
		return sdk.NewError(http.StatusConflict, "no_password", "the dashboard has no password")
	case a.Presence == nil:
		a.mu.Unlock()
		return sdk.NewError(http.StatusConflict, "no_presence", "the console plugin, which sees the device's buttons, is not available")
	case a.reset.active:
		a.mu.Unlock()
		return nil
	}
	a.reset = resetState{active: true, until: time.Now().Add(time.Minute)}
	presence := a.Presence
	a.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		err := presence(ctx)
		a.mu.Lock()
		defer a.mu.Unlock()
		a.reset.active = false
		if err != nil {
			a.reset.err = err.Error()
			return
		}
		if rerr := os.Remove(a.path); rerr != nil && !os.IsNotExist(rerr) {
			a.reset.err = rerr.Error()
			return
		}
		a.data, a.modTime = nil, time.Time{}
		a.reset.done = time.Now()
	}()
	return nil
}

// status for GET api/access.
func (a *Access) status(r *http.Request) map[string]interface{} {
	protected := a.Protected()
	doc := map[string]interface{}{"protected": protected, "loggedIn": protected && a.LoggedIn(r), "fromDevice": fromDevice(r)}
	a.mu.Lock()
	defer a.mu.Unlock()
	rs := map[string]interface{}{"active": a.reset.active}
	if a.reset.active {
		rs["seconds"] = int(time.Until(a.reset.until).Seconds()) + 1
	}
	if !a.reset.done.IsZero() && time.Since(a.reset.done) < 5*time.Minute {
		rs["done"] = a.reset.done.Format(time.RFC3339)
	}
	if a.reset.err != "" {
		rs["error"] = a.reset.err
	}
	doc["reset"] = rs
	return doc
}

// --- HTTP

func (s *Server) handleAccess(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.Access.status(r))
}

func readJSON(w http.ResponseWriter, r *http.Request, v interface{}) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		sdk.WriteError(w, sdk.NewError(http.StatusBadRequest, "bad_request", "invalid request body"))
		return false
	}
	return true
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	v, err := s.Access.Login(req.Password)
	if err != nil {
		sdk.WriteError(w, err)
		return
	}
	setSession(w, r, v, int(sessionLife.Seconds()))
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	setSession(w, r, "", -1)
	writeJSON(w, map[string]bool{"ok": true})
}

// handlePassword sets, changes or removes the password: {"current","new"}.
// Behind the gate: with a password set, only a logged-in visitor gets here.
func (s *Server) handlePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.Access.SetPassword(req.Current, req.New); err != nil {
		sdk.WriteError(w, err)
		return
	}
	if req.New != "" {
		// The new key ended every session; this browser stays logged in.
		if v, err := s.Access.Login(req.New); err == nil {
			setSession(w, r, v, int(sessionLife.Seconds()))
		}
	} else {
		setSession(w, r, "", -1)
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleAccessReset(w http.ResponseWriter, r *http.Request) {
	if err := s.Access.StartReset(); err != nil {
		sdk.WriteError(w, err)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true, "need": 4, "seconds": 60})
}

// handleLoginPage serves the dashboard's login page.
func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if s.dashboard == nil {
		http.Error(w, "log in with POST /api/login", http.StatusOK)
		return
	}
	http.ServeFileFS(w, r, s.dashboard, "login.html")
}

var errNoConsole = errors.New("the console plugin is not running")

// PresenceViaConsole asks the console plugin, directly over the loopback
// interface (not through the proxy, so no visitor can answer for it), to
// wait until channel 1's Listen button is pressed four times at the device.
func PresenceViaConsole(m *Manager) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		var port int
		for _, st := range m.List() {
			if st.Name == "console" && st.Running {
				port = st.Port
			}
		}
		if port == 0 {
			return errNoConsole
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:"+strconv.Itoa(port)+"/api/presence", nil)
		if err != nil {
			return err
		}
		req.Header.Set(sdk.APIHeader, "1")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return errors.New("time is up; nothing was changed")
			}
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			var e struct {
				Error string `json:"error"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&e)
			if e.Error == "" {
				e.Error = resp.Status
			}
			return errors.New(e.Error)
		}
		return nil
	}
}
