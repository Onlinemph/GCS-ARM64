package main

import (
	"embed"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed pages/*.html
var pageFS embed.FS

var indexTmpl = template.Must(template.ParseFS(pageFS, "pages/index.html"))

type indexData struct {
	Library []*sheet
	Uploads []*sheet
	Error   string
}

// sheetView is the shape the index page iterates over.
func (s *sheet) Title() string { return s.name }
func (s *sheet) URL() string   { return "/sheet/" + s.id }

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	uploads := make([]*sheet, 0, len(s.uploads))
	for _, sh := range s.uploads {
		uploads = append(uploads, sh)
	}
	s.mu.RUnlock()

	data := indexData{
		Library: s.librarySheets(),
		Uploads: uploads,
		Error:   r.URL.Query().Get("error"),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := indexTmpl.Execute(w, data); err != nil {
		log.Printf("render index: %v", err)
	}
}

func (s *server) handleUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxUpload)
	if err := r.ParseMultipartForm(s.maxUpload); err != nil {
		redirectWithError(w, r, "Upload too large or malformed.")
		return
	}
	file, header, err := r.FormFile("sheet")
	if err != nil {
		redirectWithError(w, r, "No file was selected.")
		return
	}
	defer func() { _ = file.Close() }()

	if !strings.EqualFold(filepath.Ext(header.Filename), ".gcs") {
		redirectWithError(w, r, "That is not a .gcs character sheet.")
		return
	}

	id := randomID()
	// The stored name is server-generated; the client's file name is never
	// used as a path component.
	path := filepath.Join(s.scratchDir, "upload-"+id+".gcs")
	dst, err := os.Create(path)
	if err != nil {
		log.Printf("create upload: %v", err)
		redirectWithError(w, r, "Could not store the sheet.")
		return
	}
	if _, err = io.Copy(dst, file); err != nil {
		_ = dst.Close()
		_ = os.Remove(path)
		redirectWithError(w, r, "Could not store the sheet.")
		return
	}
	if err = dst.Close(); err != nil {
		_ = os.Remove(path)
		redirectWithError(w, r, "Could not store the sheet.")
		return
	}

	sh := &sheet{
		id:       id,
		name:     strings.TrimSuffix(filepath.Base(header.Filename), filepath.Ext(header.Filename)),
		path:     path,
		uploaded: true,
		added:    time.Now(),
	}
	// Reject files the model cannot load, rather than handing back a link that
	// only fails later.
	if _, err = s.render(sh, "sheet"); err != nil {
		_ = os.Remove(path)
		log.Printf("validate upload: %v", err)
		redirectWithError(w, r, "That file could not be read as a GCS character sheet.")
		return
	}

	s.mu.Lock()
	s.uploads[id] = sh
	s.mu.Unlock()

	http.Redirect(w, r, "/sheet/"+id, http.StatusSeeOther)
}

func (s *server) handleSheet(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.lookup(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	data, err := s.render(sh, "sheet")
	if err != nil {
		log.Printf("render sheet %s: %v", sh.name, err)
		http.Error(w, "Could not render this sheet.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

// handleAPISheet serves the raw .gcs document, which is already JSON in
// upstream's own schema.
//
// This deliberately does not expose the *computed* sheet (skill levels,
// weapon damage, encumbrance). Those live in upstream's unexported
// exportedEntity, reachable today only through a template. Serving them as
// JSON needs either an upstream writer-based export API or a local mapping
// layer — see docs/online-plan.md, phase 2.
func (s *server) handleAPISheet(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.lookup(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(sh.path)
	if err != nil {
		log.Printf("open sheet %s: %v", sh.name, err)
		http.Error(w, `{"error":"could not read this sheet"}`, http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if _, err = io.Copy(w, f); err != nil {
		log.Printf("stream sheet %s: %v", sh.name, err)
	}
}

func redirectWithError(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, "/?error="+url.QueryEscape(msg), http.StatusSeeOther)
}
