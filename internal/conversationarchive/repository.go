package conversationarchive

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/soju06/codex-lb/internal/httputil"
)

const (
	jsonlSuffix     = ".jsonl"
	gzipJSONLSuffix = ".jsonl.gz"
)

var (
	ErrNotFound    = errors.New("conversation archive file not found")
	ErrInvalidFile = errors.New("invalid conversation archive file name")
)

type Repository struct {
	dir string
}

func NewRepository(dir string) Repository {
	return Repository{dir: dir}
}

// File describes one archive file on disk.
type File struct {
	Name       string
	Date       string
	SizeBytes  int64
	Compressed bool
	ModifiedAt time.Time
}

// Page is a slice of matching records plus pagination metadata.
type Page struct {
	Records []map[string]any
	Total   int
	HasMore bool
}

// RecordFilter narrows which records ReadRecords returns.
type RecordFilter struct {
	Direction string
	Kind      string
	Transport string
	RequestID string
}

// ListFiles returns archive files sorted by name, most recent first.
func (r Repository) ListFiles() ([]File, error) {
	entries, err := os.ReadDir(r.dir)
	if errors.Is(err, os.ErrNotExist) {
		return httputil.EmptySlice([]File{}), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read conversation archive dir: %w", err)
	}

	var files []File
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, jsonlSuffix) && !strings.HasSuffix(name, gzipJSONLSuffix) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("stat conversation archive file %q: %w", name, err)
		}
		files = append(files, File{
			Name:       name,
			Date:       dateFromFilename(name),
			SizeBytes:  info.Size(),
			Compressed: strings.HasSuffix(name, gzipJSONLSuffix),
			ModifiedAt: info.ModTime().UTC(),
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name > files[j].Name })
	return httputil.EmptySlice(files), nil
}

// ReadRecords returns a page of records from the named archive file (or all archive files if
// filename is empty), filtered by filter and paginated by offset/limit.
func (r Repository) ReadRecords(filename string, filter RecordFilter, offset, limit int) (Page, error) {
	var paths []string
	if filename != "" {
		path, err := r.resolveFile(filename)
		if err != nil {
			return Page{}, err
		}
		paths = []string{path}
	} else {
		files, err := r.ListFiles()
		if err != nil {
			return Page{}, err
		}
		for _, f := range files {
			paths = append(paths, filepath.Join(r.dir, f.Name))
		}
		sort.Strings(paths)
	}

	var records []map[string]any
	total := 0
	end := offset + limit
	for _, path := range paths {
		if err := iterateRecords(path, func(record map[string]any) {
			if !recordMatches(record, filter) {
				return
			}
			if total >= offset && total < end {
				record["_archive_file"] = filepath.Base(path)
				records = append(records, record)
			}
			total++
		}); err != nil {
			return Page{}, err
		}
	}

	return Page{
		Records: httputil.EmptySlice(records),
		Total:   total,
		HasMore: end < total,
	}, nil
}

func (r Repository) resolveFile(filename string) (string, error) {
	if filepath.Base(filename) != filename ||
		!(strings.HasSuffix(filename, jsonlSuffix) || strings.HasSuffix(filename, gzipJSONLSuffix)) {
		return "", ErrInvalidFile
	}
	path := filepath.Join(r.dir, filename)
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) || (err == nil && info.IsDir()) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("stat conversation archive file %q: %w", filename, err)
	}
	return path, nil
}

func iterateRecords(path string, fn func(record map[string]any)) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open conversation archive file %q: %w", path, err)
	}
	defer f.Close()

	var scanner *bufio.Scanner
	if strings.HasSuffix(path, gzipJSONLSuffix) {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil //nolint:nilerr // matches Python behavior of skipping unreadable archives
		}
		defer gz.Close()
		scanner = bufio.NewScanner(gz)
	} else {
		scanner = bufio.NewScanner(f)
	}
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		fn(record)
	}
	return nil
}

func recordMatches(record map[string]any, filter RecordFilter) bool {
	if filter.Direction != "" && fmt.Sprint(record["direction"]) != filter.Direction {
		return false
	}
	if filter.Kind != "" && fmt.Sprint(record["kind"]) != filter.Kind {
		return false
	}
	if filter.Transport != "" && fmt.Sprint(record["transport"]) != filter.Transport {
		return false
	}
	if filter.RequestID != "" && fmt.Sprint(record["request_id"]) != filter.RequestID {
		return false
	}
	return true
}

func dateFromFilename(filename string) string {
	var stem string
	switch {
	case strings.HasSuffix(filename, gzipJSONLSuffix):
		stem = strings.TrimSuffix(filename, gzipJSONLSuffix)
	case strings.HasSuffix(filename, jsonlSuffix):
		stem = strings.TrimSuffix(filename, jsonlSuffix)
	default:
		return ""
	}
	for _, layout := range []string{"2006-01-02T15", "2006-01-02"} {
		if _, err := time.Parse(layout, stem); err == nil {
			return stem
		}
	}
	return ""
}
