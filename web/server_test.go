package main

import (
	"encoding/hex"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardwilkes/gcs/v5/model/gurps"
)

// newTestLibrary writes a minimal but real character sheet, built by the
// upstream model itself, so the tests exercise the true load/render path
// without shipping any third-party sheet data.
func newTestLibrary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	entity := gurps.NewEntity()
	entity.Profile.Name = "Test Subject"
	entity.Profile.PlayerName = "Tester"
	if err := entity.Save(filepath.Join(dir, "test.gcs")); err != nil {
		t.Fatalf("save fixture: %v", err)
	}
	return dir
}

func newTestServer(t *testing.T) (*server, http.Handler) {
	t.Helper()
	srv, err := newServer(newTestLibrary(t), 8<<20)
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	t.Cleanup(srv.close)
	return srv, srv.routes()
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestIndexListsLibrarySheets(t *testing.T) {
	_, h := newTestServer(t)
	rec := get(t, h, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "/sheet/lib-") {
		t.Error("index does not link to the library sheet")
	}
}

func TestSheetRendersThroughUpstreamModel(t *testing.T) {
	_, h := newTestServer(t)
	id := "lib-" + hex.EncodeToString([]byte("test.gcs"))
	rec := get(t, h, "/sheet/"+id)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Test Subject", // profile came through
		"<nav",         // tab bar rendered
		"Basic Lift",   // a value only the rules engine can produce
		"viewport",     // mobile meta tag
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered sheet is missing %q", want)
		}
	}
	// html/template writes this marker when it refuses to emit a value; its
	// presence means a template expression landed in the wrong context.
	if strings.Contains(body, "ZgotmplZ") {
		t.Error("rendered sheet contains an escaping failure (ZgotmplZ)")
	}
}

func TestUnknownSheetIs404(t *testing.T) {
	_, h := newTestServer(t)
	for _, path := range []string{
		"/sheet/lib-" + hex.EncodeToString([]byte("missing.gcs")),
		"/sheet/lib-nothex",
		"/sheet/does-not-exist",
	} {
		if rec := get(t, h, path); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", path, rec.Code)
		}
	}
}

// Library IDs are hex-encoded file names, so the decoder is what stands
// between a crafted ID and the rest of the filesystem.
func TestLibraryIDCannotEscapeTheLibraryDirectory(t *testing.T) {
	_, h := newTestServer(t)
	for _, name := range []string{
		"../../etc/passwd",
		"../test.gcs",
		"/etc/passwd",
		"sub/dir/test.gcs",
	} {
		path := "/sheet/lib-" + hex.EncodeToString([]byte(name))
		if rec := get(t, h, path); rec.Code != http.StatusNotFound {
			t.Errorf("%q: status = %d, want 404", name, rec.Code)
		}
	}
}

func TestAPIServesTheRawDocument(t *testing.T) {
	_, h := newTestServer(t)
	id := "lib-" + hex.EncodeToString([]byte("test.gcs"))
	rec := get(t, h, "/api/sheet/"+id)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}
	if !strings.Contains(rec.Body.String(), "Test Subject") {
		t.Error("raw document does not contain the character name")
	}
}

func uploadRequest(t *testing.T, fileName string, content []byte) *http.Request {
	t.Helper()
	var body strings.Builder
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("sheet", fileName)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err = part.Write(content); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err = w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader(body.String()))
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

func TestUploadRoundTrip(t *testing.T) {
	srv, h := newTestServer(t)
	content, err := os.ReadFile(filepath.Join(srv.libraryDir, "test.gcs"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, uploadRequest(t, "hero.gcs", content))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/sheet/") {
		t.Fatalf("Location = %q, want a /sheet/ redirect", loc)
	}
	if got := get(t, h, loc); got.Code != http.StatusOK {
		t.Fatalf("following redirect: status = %d, want 200", got.Code)
	}
}

func TestUploadRejectsFilesTheModelCannotRead(t *testing.T) {
	_, h := newTestServer(t)
	for _, tc := range []struct {
		name     string
		fileName string
		content  []byte
	}{
		{"wrong extension", "notes.txt", []byte("hello")},
		{"not a sheet", "hero.gcs", []byte("this is not JSON")},
		{"empty", "hero.gcs", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, uploadRequest(t, tc.fileName, tc.content))
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("status = %d, want 303", rec.Code)
			}
			if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/?error=") {
				t.Errorf("Location = %q, want an error redirect", loc)
			}
		})
	}
}

// A rejected upload must not leave its bytes behind in the scratch directory.
func TestRejectedUploadLeavesNoFiles(t *testing.T) {
	srv, h := newTestServer(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, uploadRequest(t, "hero.gcs", []byte("this is not JSON")))

	entries, err := os.ReadDir(srv.scratchDir)
	if err != nil {
		t.Fatalf("read scratch dir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "upload-") {
			t.Errorf("rejected upload left %s behind", entry.Name())
		}
	}
}

func TestHealthz(t *testing.T) {
	_, h := newTestServer(t)
	rec := get(t, h, "/healthz")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body, _ := io.ReadAll(rec.Body); strings.TrimSpace(string(body)) != "ok" {
		t.Errorf("body = %q, want ok", body)
	}
}
