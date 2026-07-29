package main

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/richardwilkes/gcs/v5/model/gurps"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

// uploadTTL is how long an uploaded sheet stays available before the janitor
// removes it. Uploads are scratch data: the browser keeps the link, the server
// keeps the bytes only as long as someone is plausibly still reading them.
const uploadTTL = 6 * time.Hour

// sheet is one loadable .gcs file. Library sheets live in the library
// directory and persist; uploaded sheets live in the server's scratch
// directory and expire.
type sheet struct {
	id       string
	name     string
	path     string
	uploaded bool
	added    time.Time
}

type server struct {
	libraryDir string
	scratchDir string
	maxUpload  int64

	// tmplPaths maps a template name ("mobile", "json") to the on-disk copy
	// the upstream exporter reads. gurps.Export takes template and output
	// *paths*, not writers, so the embedded templates are materialised once at
	// startup rather than per request.
	tmplPaths map[string]string

	mu      sync.RWMutex
	uploads map[string]*sheet
}

func newServer(libraryDir string, maxUpload int64) (*server, error) {
	scratch, err := os.MkdirTemp("", "gcsweb-")
	if err != nil {
		return nil, fmt.Errorf("create scratch dir: %w", err)
	}
	s := &server{
		libraryDir: libraryDir,
		scratchDir: scratch,
		maxUpload:  maxUpload,
		tmplPaths:  make(map[string]string),
		uploads:    make(map[string]*sheet),
	}
	if err := s.materialiseTemplates(); err != nil {
		_ = os.RemoveAll(scratch)
		return nil, err
	}
	go s.janitor()
	return s, nil
}

func (s *server) close() {
	_ = os.RemoveAll(s.scratchDir)
}

// materialiseTemplates writes the embedded export templates into the scratch
// directory so the upstream exporter can read them by path.
func (s *server) materialiseTemplates() error {
	entries, err := templateFS.ReadDir("templates")
	if err != nil {
		return fmt.Errorf("read embedded templates: %w", err)
	}
	for _, entry := range entries {
		data, err := templateFS.ReadFile("templates/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read embedded template %s: %w", entry.Name(), err)
		}
		// "sheet.html.tmpl" -> name "sheet.html", so the exporter picks the
		// html/template engine from the extension it ends up seeing.
		outName := strings.TrimSuffix(entry.Name(), ".tmpl")
		outPath := filepath.Join(s.scratchDir, outName)
		if err := os.WriteFile(outPath, data, 0o600); err != nil {
			return fmt.Errorf("write template %s: %w", outName, err)
		}
		s.tmplPaths[strings.SplitN(outName, ".", 2)[0]] = outPath
	}
	if _, ok := s.tmplPaths["sheet"]; !ok {
		return fmt.Errorf("missing embedded template %q", "sheet")
	}
	return nil
}

// janitor drops expired uploads and their files.
func (s *server) janitor() {
	for range time.Tick(30 * time.Minute) {
		cutoff := time.Now().Add(-uploadTTL)
		s.mu.Lock()
		for id, sh := range s.uploads {
			if sh.added.Before(cutoff) {
				_ = os.Remove(sh.path)
				delete(s.uploads, id)
			}
		}
		s.mu.Unlock()
	}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("POST /upload", s.handleUpload)
	mux.HandleFunc("GET /sheet/{id}", s.handleSheet)
	mux.HandleFunc("GET /api/sheet/{id}", s.handleAPISheet)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok\n")
	})
	return mux
}

// librarySheets lists the .gcs files in the library directory. IDs are derived
// from the file name so links stay stable across restarts.
func (s *server) librarySheets() []*sheet {
	entries, err := os.ReadDir(s.libraryDir)
	if err != nil {
		return nil
	}
	var sheets []*sheet
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".gcs") {
			continue
		}
		name := entry.Name()
		sheets = append(sheets, &sheet{
			id:   "lib-" + hex.EncodeToString([]byte(name)),
			name: strings.TrimSuffix(name, filepath.Ext(name)),
			path: filepath.Join(s.libraryDir, name),
		})
	}
	sort.Slice(sheets, func(i, j int) bool { return sheets[i].name < sheets[j].name })
	return sheets
}

// lookup resolves a sheet ID to a sheet, for both library and uploaded sheets.
func (s *server) lookup(id string) (*sheet, bool) {
	if strings.HasPrefix(id, "lib-") {
		raw, err := hex.DecodeString(strings.TrimPrefix(id, "lib-"))
		if err != nil {
			return nil, false
		}
		name := string(raw)
		// Reject anything that is not a plain file name in the library dir.
		if name != filepath.Base(name) || !strings.EqualFold(filepath.Ext(name), ".gcs") {
			return nil, false
		}
		path := filepath.Join(s.libraryDir, name)
		if _, err := os.Stat(path); err != nil {
			return nil, false
		}
		return &sheet{
			id:   id,
			name: strings.TrimSuffix(name, filepath.Ext(name)),
			path: path,
		}, true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	sh, ok := s.uploads[id]
	return sh, ok
}

// render runs a sheet through the upstream exporter with the named template
// and returns the result.
//
// gurps.Export writes to a path rather than a writer, so each render goes
// through a unique scratch file. See README ("Known rough edges") — the fix is
// an upstream writer-based export API, not a fork.
func (s *server) render(sh *sheet, tmplName string) ([]byte, error) {
	tmplPath, ok := s.tmplPaths[tmplName]
	if !ok {
		return nil, fmt.Errorf("unknown template %q", tmplName)
	}
	entity, err := gurps.NewEntityFromFile(os.DirFS(filepath.Dir(sh.path)), filepath.Base(sh.path))
	if err != nil {
		return nil, fmt.Errorf("load sheet: %w", err)
	}
	outPath := filepath.Join(s.scratchDir, "render-"+randomID()+filepath.Ext(tmplPath))
	defer func() { _ = os.Remove(outPath) }()
	if err := gurps.Export(entity, tmplPath, outPath); err != nil {
		return nil, fmt.Errorf("export sheet: %w", err)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		return nil, fmt.Errorf("read rendered sheet: %w", err)
	}
	return data, nil
}

func randomID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand does not fail in practice; fall back to a time value
		// rather than taking the process down over an ID.
		return fmt.Sprintf("t%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf[:])
}
