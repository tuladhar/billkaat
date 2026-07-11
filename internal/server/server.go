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
	"time"

	"github.com/billkaat/billkaat/internal/awsx"
	"github.com/billkaat/billkaat/internal/buildinfo"
	"github.com/billkaat/billkaat/internal/checks"
	"github.com/billkaat/billkaat/internal/engine"
	"github.com/billkaat/billkaat/internal/license"
	"github.com/billkaat/billkaat/internal/store"
)

const licenseSettingKey = "license"

type Server struct {
	Store  *store.Store
	Engine *engine.Engine
	Web    fs.FS
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/meta", s.handleMeta)
	mux.HandleFunc("GET /api/identity", s.handleIdentity)
	mux.HandleFunc("POST /api/scan", s.handleStartScan)
	mux.HandleFunc("GET /api/scan/{id}", s.handleGetScan)
	mux.HandleFunc("GET /api/scan/{id}/export.csv", s.handleExportCSV)
	mux.HandleFunc("GET /api/scans", s.handleListScans)
	mux.HandleFunc("POST /api/license", s.handleLicenseActivate)
	mux.HandleFunc("DELETE /api/license", s.handleLicenseRemove)
	mux.Handle("GET /", http.FileServerFS(s.Web))
	return mux
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

// ---- identity (which account am I about to scan?) ----

func (s *Server) handleIdentity(w http.ResponseWriter, r *http.Request) {
	region := r.URL.Query().Get("region")
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	clients, err := awsx.New(ctx, region)
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
		Region string `json:"region"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Region == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body must be {\"region\": \"...\"}"})
		return
	}
	if running, _ := s.Store.RunningScan(); running != 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "a scan is already running", "scan_id": running})
		return
	}
	id, err := s.Engine.StartScan(body.Region)
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
