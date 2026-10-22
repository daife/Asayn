package tools

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/asayn/asayn/internal/llm/types"
)

// Keep inline images below the common provider limits, including base64 overhead.
const maxImageBytes = 20 * 1024 * 1024

func (e *Executor) ReadImage(args map[string]any) (string, []types.ImageInput, error) {
	detail := stringArg(args, "detail")
	if detail == "" {
		detail = "auto"
	}
	switch detail {
	case "auto", "low", "high", "original":
	default:
		return "", nil, fmt.Errorf("invalid image detail %q", detail)
	}
	path, err := e.resolveWorkspaceRootPath(stringArg(args, "path"))
	if err != nil {
		return "", nil, err
	}
	// Apply the workspace boundary to symlink targets as well.
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", nil, err
	}
	root, err := filepath.EvalSymlinks(e.paths.WorkspaceRoot)
	if err != nil {
		return "", nil, err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || (len(rel) > 3 && rel[:3] == ".."+string(filepath.Separator)) {
		return "", nil, fmt.Errorf("image path escapes workspace")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", nil, err
	}
	if !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("image must be a regular file")
	}
	if info.Size() > maxImageBytes {
		return "", nil, fmt.Errorf("image exceeds 20 MiB limit")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxImageBytes+1))
	if err != nil {
		return "", nil, err
	}
	if len(data) > maxImageBytes {
		return "", nil, fmt.Errorf("image exceeds 20 MiB limit")
	}
	mime := http.DetectContentType(data)
	switch mime {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		return "", nil, fmt.Errorf("unsupported image content %q; use PNG, JPEG, GIF or WebP", mime)
	}
	image := types.ImageInput{URL: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), Detail: detail}
	return fmt.Sprintf("Image loaded: %s (%s, %d bytes, detail=%s)", rel, mime, len(data), detail), []types.ImageInput{image}, nil
}
