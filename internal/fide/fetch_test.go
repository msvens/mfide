package fide

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/msvens/mfide/internal/dao"
)

// fileServer serves body with an ETag, honoring If-None-Match unless ignoreConditional.
type fileServer struct {
	body              string
	etag              string
	ignoreConditional bool
	lastIfNoneMatch   string
}

func (s *fileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.lastIfNoneMatch = r.Header.Get("If-None-Match")
	if !s.ignoreConditional && s.lastIfNoneMatch == s.etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", s.etag)
	w.Header().Set("Last-Modified", "Thu, 22 Oct 2026 21:08:05 GMT")
	_, _ = w.Write([]byte(s.body))
}

// fakeFiles is an in-memory sourceFiles.
type fakeFiles struct {
	files   []*dao.SourceFile
	touched []int
}

func (f *fakeFiles) Latest(url string) (*dao.SourceFile, error) {
	for i := len(f.files) - 1; i >= 0; i-- {
		if f.files[i].Url == url {
			return f.files[i], nil
		}
	}
	return nil, sql.ErrNoRows
}

func (f *fakeFiles) Insert(file *dao.SourceFile) error {
	file.Id = len(f.files) + 1
	f.files = append(f.files, file)
	return nil
}

func (f *fakeFiles) Touch(id int) error {
	f.touched = append(f.touched, id)
	return nil
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	names := []string{}
	for _, e := range es {
		names = append(names, e.Name())
	}
	return names
}

func TestDownloadLifecycle(t *testing.T) {
	srv := &fileServer{body: "version one", etag: `"v1"`}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	root := t.TempDir()
	f := NewFetcher(root)
	files := &fakeFiles{}
	target := Target{Kind: dao.SourceCurrent, Url: ts.URL + "/players_list_xml.zip", DestRel: "downloads/current/day1/players_list_xml.zip"}

	// First fetch: new content on disk, recorded with its headers.
	out, file, err := Download(f, files, target)
	if err != nil || out != New {
		t.Fatalf("first download = (%s, %v), want new", out, err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, file.Path)); string(b) != "version one" {
		t.Errorf("stored content %q", b)
	}
	if file.Etag != `"v1"` || file.LastModified == nil || file.Size != int64(len("version one")) || len(file.Sha256) != 64 {
		t.Errorf("recorded file %+v", file)
	}

	// Second fetch is conditional: 304, only a checked_at bump.
	out, _, err = Download(f, files, target)
	if err != nil || out != NotModified {
		t.Fatalf("second download = (%s, %v), want not modified", out, err)
	}
	if srv.lastIfNoneMatch != `"v1"` {
		t.Errorf("If-None-Match = %q, want the stored ETag", srv.lastIfNoneMatch)
	}
	if len(files.files) != 1 || len(files.touched) != 1 {
		t.Errorf("files %d, touched %d; want 1 and 1", len(files.files), len(files.touched))
	}

	// A server that ignores conditionals but sends the same bytes: unchanged, no stray files.
	srv.ignoreConditional = true
	if out, _, err = Download(f, files, target); err != nil || out != Unchanged {
		t.Fatalf("same bytes = (%s, %v), want unchanged", out, err)
	}
	if names := entries(t, filepath.Join(root, "downloads/current/day1")); len(names) != 1 {
		t.Errorf("dir holds %v, want only the first file", names)
	}

	// New bytes on the same day: stored next to the first with a sha suffix.
	srv.body, srv.etag = "version two", `"v2"`
	out, file, err = Download(f, files, target)
	if err != nil || out != New {
		t.Fatalf("changed bytes = (%s, %v), want new", out, err)
	}
	if file.Path != target.DestRel+"."+file.Sha256[:8] {
		t.Errorf("second version stored at %s, want a .<sha8> suffix", file.Path)
	}
	if b, _ := os.ReadFile(filepath.Join(root, target.DestRel)); string(b) != "version one" {
		t.Error("the first version must not be overwritten")
	}
}

func TestFetchErrorsLeaveNoFiles(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing.zip" {
			http.NotFound(w, r)
			return
		}
		// Promise more than we send, then drop the connection.
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write([]byte("partial"))
		panic(http.ErrAbortHandler)
	}))
	defer ts.Close()
	root := t.TempDir()
	f := NewFetcher(root)

	for _, path := range []string{"/missing.zip", "/broken.zip"} {
		_, _, err := f.Fetch(ts.URL+path, "downloads/x/file.zip", nil)
		if err == nil {
			t.Errorf("%s: expected an error", path)
		}
		// Sync treats a 404 as "not published yet", so only a 404 may be ErrNotFound.
		if notFound := errors.Is(err, ErrNotFound); notFound != (path == "/missing.zip") {
			t.Errorf("%s: errors.Is(%v, ErrNotFound) = %v", path, err, notFound)
		}
	}
	dir := filepath.Join(root, "downloads/x")
	if _, err := os.Stat(dir); err == nil {
		if names := entries(t, dir); len(names) != 0 {
			t.Errorf("failed downloads left %v behind", names)
		}
	}
}

func TestURLs(t *testing.T) {
	base := "https://ratings.fide.com/download"
	if got := CurrentURL(base); got != base+"/players_list_xml.zip" {
		t.Errorf("CurrentURL = %s", got)
	}
	cases := []struct {
		typ  string
		y, m int
		want string
	}{
		{"standard", 2026, 9, "standard_sep26frl_xml.zip"},
		{"rapid", 2015, 2, "rapid_feb15frl_xml.zip"},
		{"blitz", 2027, 1, "blitz_jan27frl_xml.zip"},
		{"standard", 2030, 12, "standard_dec30frl_xml.zip"},
	}
	for _, c := range cases {
		p := time.Date(c.y, time.Month(c.m), 1, 0, 0, 0, 0, time.UTC)
		if got := ArchiveURL(base, c.typ, p); got != base+"/"+c.want {
			t.Errorf("ArchiveURL(%s, %d-%02d) = %s, want %s", c.typ, c.y, c.m, got, c.want)
		}
	}
}
