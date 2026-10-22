package tools

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asayn/asayn/internal/config"
)

func TestReadImage(t *testing.T) {
	root := t.TempDir()
	e := &Executor{paths: config.Paths{WorkspaceRoot: root}}
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "image.bin"), data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	out, images, err := e.ReadImage(map[string]any{"path": "image.bin"})
	if err != nil || len(images) != 1 || !strings.HasPrefix(images[0].URL, "data:image/png;base64,") || strings.Contains(out, "base64") {
		t.Fatalf("%s %+v %v", out, images, err)
	}
	for _, args := range []map[string]any{{}, {"path": "../outside.png"}, {"path": "image.bin", "detail": "bad"}, {"path": "."}} {
		if _, _, err := e.ReadImage(args); err == nil {
			t.Fatalf("accepted invalid args: %v", args)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "fake.png"), []byte("not an image"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.ReadImage(map[string]any{"path": "fake.png"}); err == nil {
		t.Fatal("accepted text as image")
	}
	outside := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(outside, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.png")); err == nil {
		if _, _, err := e.ReadImage(map[string]any{"path": "link.png"}); err == nil {
			t.Fatal("accepted escaping symlink")
		}
	}
	f, err := os.Create(filepath.Join(root, "large.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxImageBytes + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	if _, _, err := e.ReadImage(map[string]any{"path": "large.png"}); err == nil {
		t.Fatal("accepted oversized image")
	}
}
