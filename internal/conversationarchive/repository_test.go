package conversationarchive_test

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/soju06/codex-lb/internal/conversationarchive"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if filepath.Ext(name) == ".gz" {
		f, err := os.Create(path)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		defer f.Close()
		gz := gzip.NewWriter(f)
		defer gz.Close()
		if _, err := gz.Write([]byte(content)); err != nil {
			t.Fatalf("write gzip %s: %v", name, err)
		}
		return
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestRepositoryListAndReadRecords(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "2026-06-01.jsonl", `{"direction":"request","kind":"chat","request_id":"r1"}
{"direction":"response","kind":"chat","request_id":"r1"}
not-json
{"direction":"request","kind":"other","request_id":"r2"}
`)
	writeFile(t, dir, "2026-06-02T00.jsonl.gz", `{"direction":"request","kind":"chat","request_id":"r3"}
`)

	repo := conversationarchive.NewRepository(dir)

	files, err := repo.ListFiles()
	if err != nil {
		t.Fatalf("list files: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d: %#v", len(files), files)
	}
	if files[0].Name != "2026-06-02T00.jsonl.gz" {
		t.Fatalf("expected most recent file first, got %q", files[0].Name)
	}
	if !files[0].Compressed {
		t.Fatalf("expected gz file to be marked compressed")
	}
	if files[0].Date != "2026-06-02T00" {
		t.Fatalf("expected date 2026-06-02T00, got %q", files[0].Date)
	}

	page, err := repo.ReadRecords("", conversationarchive.RecordFilter{}, 0, 10)
	if err != nil {
		t.Fatalf("read records: %v", err)
	}
	if page.Total != 4 {
		t.Fatalf("expected 4 total records (skipping invalid line), got %d", page.Total)
	}
	if len(page.Records) != 4 {
		t.Fatalf("expected 4 returned records, got %d", len(page.Records))
	}

	page, err = repo.ReadRecords("", conversationarchive.RecordFilter{Kind: "chat"}, 0, 10)
	if err != nil {
		t.Fatalf("read records filtered: %v", err)
	}
	if page.Total != 3 {
		t.Fatalf("expected 3 chat records, got %d", page.Total)
	}

	page, err = repo.ReadRecords("2026-06-01.jsonl", conversationarchive.RecordFilter{}, 0, 1)
	if err != nil {
		t.Fatalf("read records single file: %v", err)
	}
	if page.Total != 3 || !page.HasMore {
		t.Fatalf("expected total=3 hasMore=true, got total=%d hasMore=%v", page.Total, page.HasMore)
	}

	if _, err := repo.ReadRecords("missing.jsonl", conversationarchive.RecordFilter{}, 0, 1); err != conversationarchive.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if _, err := repo.ReadRecords("../escape.jsonl", conversationarchive.RecordFilter{}, 0, 1); err != conversationarchive.ErrInvalidFile {
		t.Fatalf("expected ErrInvalidFile, got %v", err)
	}
}
