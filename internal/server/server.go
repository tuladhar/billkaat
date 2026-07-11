// Package server is the HTTP layer: a JSON API plus the embedded static UI.
// It binds to localhost by default — this tool is single-user and local.
package server

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/billkaat/billkaat/internal/auth"
	"github.com/billkaat/billkaat/internal/awsx"
	"github.com/billkaat/billkaat/internal/buildinfo"
	"github.com/billkaat/billkaat/internal/checks"
	"github.com/billkaat/billkaat/internal/engine"
	"github.com/billkaat/billkaat/internal/license"
	"github.com/billkaat/billkaat/internal/store"
)

const (
	licenseSettingKey = "license"
	sessionCookieName = "billkaat_session"
	sessionTTL        = 24 * time.Hour
)

// session pairs a login with the AES key derived from that login's password.
// The key only ever lives here, in memory — restarting the process (or
// logging out) forgets it, which is what keeps AWS secrets in the database
// unreadable without the password.
type session struct {
	key     []byte
	expires time.Time
}

type Server struct {
	Store     *store.Store
	Engine    *engine.Engine
	Web       fs.FS
	IAMPolicy string

	mu       sync.Mutex
	sessions map[string]session
}

func (s *Server) Handler() http.Handler {
	s.sessions = map[string]session{}

	mux := http.NewServeMux()

	// Auth bootstrap — reachable before login.
	mux.HandleFunc("GET /api/auth/status", s.handleAuthStatus)
	mux.HandleFunc("POST /api/auth/setup", s.handleAuthSetup)
	mux.HandleFunc("POST /api/auth/login", s.handleAuthLogin)
	mux.HandleFunc("POST /api/auth/logout", s.handleAuthLogout)

	// Everything else requires a valid session.
	mux.HandleFunc("GET /api/meta", s.requireAuth(s.handleMeta))
	mux.HandleFunc("GET /api/identity", s.requireAuth(s.handleIdentity))
	mux.HandleFunc("POST /api/scan", s.requireAuth(s.handleStartScan))
	mux.HandleFunc("GET /api/scan/{id}", s.requireAuth(s.handleGetScan))
	mux.HandleFunc("GET /api/scan/{id}/export.csv", s.requireAuth(s.handleExportCSV))
	mux.HandleFunc("GET /api/scans", s.requireAuth(s.handleListScans))
	mux.HandleFunc("GET /api/accounts", s.requireAuth(s.handleListAccounts))
	mux.HandleFunc("POST /api/accounts", s.requireAuth(s.handleCreateAccount))
	mux.HandleFunc("DELETE /api/accounts/{id}", s.requireAuth(s.handleDeleteAccount))
	mux.HandleFunc("GET /api/iam-policy", s.requireAuth(s.handleIAMPolicy))
	mux.HandleFunc("POST /api/license", s.requireAuth(s.handleLicenseActivate))
	mux.HandleFunc("DELETE /api/license", s.requireAuth(s.handleLicenseRemove))

	mux.Handle("GET /", http.FileServerFS(s.Web))
	return mux
}

// ---- auth ----

type sessionKeyCtx struct{}

// requireAuth rejects requests without a live session and, for the ones
// that pass, attaches that session's decryption key to the request context.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookieName)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "log in required"})
			return
		}
		s.mu.Lock()
		sess, ok := s.sessions[c.Value]
		s.mu.Unlock()
		if !ok || time.Now().After(sess.expires) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "session expired — log in again"})
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), sessionKeyCtx{}, sess.key)))
	}
}

func sessionKey(r *http.Request) []byte {
	k, _ := r.Context().Value(sessionKeyCtx{}).([]byte)
	return k
}

func (s *Server) startSession(w http.ResponseWriter, key []byte) error {
	token, err := auth.NewSessionToken()
	if err != nil {
		return err
	}
	exp := time.Now().Add(sessionTTL)
	s.mu.Lock()
	s.sessions[token] = session{key: key, expires: exp}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Expires:  exp,
	})
	return nil
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	hasUser, err := s.Store.HasUser()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	authenticated := false
	if c, err := r.Cookie(sessionCookieName); err == nil {
		s.mu.Lock()
		sess, ok := s.sessions[c.Value]
		s.mu.Unlock()
		authenticated = ok && time.Now().Before(sess.expires)
	}
	writeJSON(w, http.StatusOK, map[string]bool{
		"setup_required": !hasUser,
		"authenticated":  authenticated,
	})
}

func (s *Server) handleAuthSetup(w http.ResponseWriter, r *http.Request) {
	hasUser, err := s.Store.HasUser()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if hasUser {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "setup already completed — log in instead"})
		return
	}
	var body struct{ Username, Password string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
		body.Username == "" || len(body.Password) < 8 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "username and a password of at least 8 characters are required"})
		return
	}
	salt, err := auth.NewSalt()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := s.Store.CreateUser(body.Username, hash, salt); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	key, err := auth.DeriveKey(body.Password, salt)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := s.startSession(w, key); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	var body struct{ Username, Password string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	u, err := s.Store.GetUser()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if u == nil || u.Username != body.Username || !auth.CheckPassword(u.PasswordHash, body.Password) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "wrong username or password"})
		return
	}
	key, err := auth.DeriveKey(body.Password, u.KDFSalt)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := s.startSession(w, key); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		s.mu.Lock()
		delete(s.sessions, c.Value)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- meta ----

type licenseStatus struct {
	Valid bool   `json:"valid"`
	Email string `json:"email,omitempty"`
	Name  string `json:"name,omitempty"`
	Plan  string `json:"plan,omitempty"`
}

func (s *Server) licenseStatus() licenseStatus {
	key, err := s.Store.GetSetting(licenseSettingKey)
	if err != nil || key == "" {
		return licenseStatus{}
	}
	p, err := license.Verify(key)
	if err != nil {
		return licenseStatus{}
	}
	return licenseStatus{Valid: true, Email: p.Email, Name: p.Name, Plan: p.Plan}
}

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	metas := []checks.Meta{}
	for _, c := range checks.All() {
		metas = append(metas, c.Meta())
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"app":            "billkaat",
		"version":        buildinfo.Version,
		"edition":        buildinfo.Edition,
		"pro_build":      buildinfo.Pro,
		"default_region": awsx.DefaultRegion(),
		"regions":        awsx.Regions,
		"checks":         metas,
		"license":        s.licenseStatus(),
	})
}

// ---- AWS accounts ----

func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	accts, err := s.Store.ListAWSAccounts()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, accts)
}

func (s *Server) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name            string `json:"name"`
		AccountID       string `json:"account_id"`
		AccessKeyID     string `json:"access_key_id"`
		SecretAccessKey string `json:"secret_access_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
		body.Name == "" || body.AccessKeyID == "" || body.SecretAccessKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "name, access_key_id and secret_access_key are required"})
		return
	}
	secretEnc, err := auth.Encrypt(sessionKey(r), []byte(body.SecretAccessKey))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	id, err := s.Store.CreateAWSAccount(body.Name, body.AccountID, body.AccessKeyID, secretEnc)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad account id"})
		return
	}
	if err := s.Store.DeleteAWSAccount(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"removed": true})
}

func (s *Server) handleIAMPolicy(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"policy": s.IAMPolicy})
}

// resolveCredentials decrypts a saved account's secret using the current
// session's key, returning ready-to-use AWS credentials and the account's
// friendly name (for labeling the scan).
func (s *Server) resolveCredentials(r *http.Request, accountID int64) (awsx.Credentials, string, error) {
	accessKeyID, name, secretEnc, err := s.Store.GetAWSAccountSecret(accountID)
	if err != nil {
		return awsx.Credentials{}, "", fmt.Errorf("AWS account not found")
	}
	secret, err := auth.Decrypt(sessionKey(r), secretEnc)
	if err != nil {
		return awsx.Credentials{}, "", err
	}
	return awsx.Credentials{AccessKeyID: accessKeyID, SecretAccessKey: string(secret)}, name, nil
}

// ---- identity (which account am I about to scan?) ----

func (s *Server) handleIdentity(w http.ResponseWriter, r *http.Request) {
	region := r.URL.Query().Get("region")
	accountID, err := strconv.ParseInt(r.URL.Query().Get("account"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "select an AWS account"})
		return
	}
	creds, _, err := s.resolveCredentials(r, accountID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	clients, err := awsx.New(ctx, region, creds)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ident, err := clients.Identity(ctx)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ident)
}

// ---- scans ----

func (s *Server) handleStartScan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Region    string `json:"region"`
		AccountID int64  `json:"account_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Region == "" || body.AccountID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "body must be {\"region\": \"...\", \"account_id\": N}"})
		return
	}
	if running, _ := s.Store.RunningScan(); running != 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "a scan is already running", "scan_id": running})
		return
	}
	creds, name, err := s.resolveCredentials(r, body.AccountID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	id, err := s.Engine.StartScan(body.Region, name, creds)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]int64{"scan_id": id})
}

func (s *Server) handleGetScan(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad scan id"})
		return
	}
	detail, err := s.Store.GetScan(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "scan not found"})
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handleListScans(w http.ResponseWriter, r *http.Request) {
	scans, err := s.Store.ListScans(25)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if scans == nil {
		scans = []store.Scan{}
	}
	writeJSON(w, http.StatusOK, scans)
}

func (s *Server) handleExportCSV(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad scan id", http.StatusBadRequest)
		return
	}
	detail, err := s.Store.GetScan(id)
	if err != nil {
		http.Error(w, "scan not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="billkaat-scan-%d.csv"`, id))
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"check", "severity", "resource_id", "resource_type",
		"region", "title", "monthly_savings_usd", "recommendation"})
	for _, f := range detail.Findings {
		_ = cw.Write([]string{f.CheckID, string(f.Severity), f.ResourceID,
			f.ResourceType, f.Region, f.Title,
			strconv.FormatFloat(f.MonthlySavingsUSD, 'f', 2, 64), f.Recommendation})
	}
	cw.Flush()
}

// ---- license ----
//
// Kept, but unenforced and unreachable from the UI while there is no paid
// tier: nothing in the product currently calls these endpoints. They stay
// so a paid Pro tier can be reintroduced later without redesigning this.

func (s *Server) handleLicenseActivate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body must be {\"key\": \"...\"}"})
		return
	}
	p, err := license.Verify(body.Key)
	if err != nil {
		msg := err.Error()
		if err == license.ErrNoPublicKey {
			msg = "this build was compiled without a license public key — see README " +
				"for building release binaries with `-ldflags -X ...PublicKeyHex=...`"
		}
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": msg})
		return
	}
	if err := s.Store.SetSetting(licenseSettingKey, body.Key); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"license":   licenseStatus{Valid: true, Email: p.Email, Name: p.Name, Plan: p.Plan},
		"pro_build": buildinfo.Pro,
	})
}

func (s *Server) handleLicenseRemove(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteSetting(licenseSettingKey); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"removed": true})
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
