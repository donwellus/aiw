package folder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/donwellus/aiw/internal/source"
)

func (d *Driver) FetchAttachments(ctx context.Context, task *source.Task, destDir string) ([]source.Attachment, error) {
	refs := source.MediaReferences(task)
	if len(refs) == 0 {
		return nil, nil
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, err
	}
	taskDir := d.base
	if task != nil {
		if path := strings.TrimSpace(task.ProviderMeta["path"]); path != "" {
			taskDir = filepath.Dir(path)
		}
	}
	attachments := make([]source.Attachment, 0, len(refs))
	for index, ref := range refs {
		if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
			return nil, fmt.Errorf("folder aceita apenas anexos locais: %s", ref)
		}
		path := strings.TrimPrefix(ref, "file://")
		if !filepath.IsAbs(path) {
			candidate := filepath.Join(d.base, filepath.FromSlash(path))
			if _, err := os.Stat(candidate); err == nil {
				path = candidate
			} else {
				path = filepath.Join(taskDir, filepath.FromSlash(path))
			}
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Size() > 10*1024*1024 {
			return nil, errors.New("anexo inválido ou acima de 10 MB: " + path)
		}
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".png", ".jpg", ".jpeg", ".webp", ".gif":
		default:
			return nil, fmt.Errorf("anexo não é imagem suportada: %s", path)
		}
		name := fmt.Sprintf("attachment-%02d%s", index+1, ext)
		target := filepath.Join(destDir, name)
		input, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			input.Close()
			return nil, err
		}
		_, copyErr := io.Copy(output, input)
		input.Close()
		closeErr := output.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		attachments = append(attachments, source.Attachment{Name: name, URL: ref, Path: target, Size: info.Size()})
	}
	for _, attachment := range attachments {
		task.Attachments = append(task.Attachments, attachment.Path)
	}
	return attachments, nil
}
