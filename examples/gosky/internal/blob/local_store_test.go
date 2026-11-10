package blob

import (
	"context"

	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Failure-mode inventory:
//
//	BL-01 round trip writes and reads bytes under tenant/folder
//	BL-02 keys cannot traverse outside the folder (../, absolute, slash)
//	BL-03 invalid tenant/folder pairs are rejected
//	BL-04 declared size mismatch (truncated/extra bytes) fails the rename
//	BL-05 a failed upload leaves no temp file behind
func TestLocalStorePutAndOpen(t *testing.T) {
	root := t.TempDir()
	store, err := NewLocalStore(root)
	if err != nil {
		t.Fatalf("new local store: %v", err)
	}
	content := "hello-gosky-file"
	reader := strings.NewReader(content)
	if err := store.Put(context.Background(), 1001, app.FileFolderCommon, "abc123.png", "image/png", reader, int64(len(content))); err != nil {
		t.Fatalf("put: %v", err)
	}

	opened, record, err := store.Open(context.Background(), 1001, app.FileFolderCommon, "abc123.png")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer opened.Close()
	data, err := io.ReadAll(opened)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != content || record.FileSize != int64(len(content)) {
		t.Fatalf("content=%q size=%d, want %q/%d", data, record.FileSize, content, len(content))
	}

	// Bytes live beneath <root>/<tenant>/<folder>, never above root.
	target, err := store.keyPath(1001, app.FileFolderCommon, "abc123.png")
	if err != nil || !filepath.IsAbs(target) {
		t.Fatalf("key path = %q, %v", target, err)
	}
	if _, err := os.Stat(filepath.Join(root, "..", "abc123.png")); !os.IsNotExist(err) {
		t.Fatalf("file escaped root: %v", err)
	}
}

func TestLocalStoreRejectsUnsafeKeys(t *testing.T) {
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("new local store: %v", err)
	}
	for _, key := range []string{"../escape", "/abs/x", "a/b", "", ".hidden", "x/../y"} {
		_, _, err := store.Open(context.Background(), 1001, app.FileFolderCommon, key)
		if err == nil {
			t.Fatalf("key %q unexpectedly accepted", key)
		}
	}
	for _, location := range []struct {
		tenant uint64
		folder uint8
	}{
		{0, 1}, {1001, 0}, {1001, 5}, {1001, 255},
	} {
		if _, _, err := store.Open(context.Background(), location.tenant, location.folder, "abc"); err == nil {
			t.Fatalf("location %+v unexpectedly accepted", location)
		}
	}
}

func TestLocalStoreRejectsSizeMismatch(t *testing.T) {
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("new local store: %v", err)
	}
	reader := strings.NewReader("short")
	if err := store.Put(context.Background(), 1001, app.FileFolderCommon, "key1", "", reader, 100); err == nil {
		t.Fatal("declared size larger than content unexpectedly accepted")
	}
	if _, _, err := store.Open(context.Background(), 1001, app.FileFolderCommon, "key1"); err == nil {
		t.Fatal("failed upload must not be readable")
	}
	entries, _ := os.ReadDir(filepath.Join(store.Root(), "1001", "1"))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".upload-") {
			t.Fatalf("temp file left behind: %s", entry.Name())
		}
	}
}
