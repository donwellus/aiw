package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/donwellus/aiw/internal/source"
)

const maxAttachmentBytes = 10 * 1024 * 1024

func (d *Driver) FetchAttachments(ctx context.Context, task *source.Task, destDir string) ([]source.Attachment, error) {
	refs := source.MediaReferences(task)
	if len(refs) == 0 {
		return nil, nil
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, err
	}
	urls, skipped := downloadableAttachmentRefs(refs)
	if len(urls) == 0 {
		return nil, fmt.Errorf("a issue não tem URL de anexo baixável; referências locais não são acessíveis pelo worker: %s", strings.Join(skipped, ", "))
	}
	tokenBytes, _ := exec.CommandContext(ctx, d.ghBin, "auth", "token").Output()
	token := strings.TrimSpace(string(tokenBytes))
	attachments := make([]source.Attachment, 0, len(urls))
	for index, ref := range urls {
		attachment, err := downloadAttachment(ctx, ref, token, destDir, index+1)
		if err != nil {
			return nil, fmt.Errorf("anexo %s: %w", ref, err)
		}
		attachments = append(attachments, attachment)
	}
	for _, attachment := range attachments {
		task.Attachments = append(task.Attachments, attachment.Path)
	}
	return attachments, nil
}

func downloadableAttachmentRefs(refs []string) ([]string, []string) {
	urls := make([]string, 0, len(refs))
	skipped := make([]string, 0)
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
			urls = append(urls, ref)
		} else if ref != "" {
			skipped = append(skipped, ref)
		}
	}
	return urls, skipped
}

func downloadAttachment(ctx context.Context, ref, token, destDir string, index int) (source.Attachment, error) {
	ref = strings.TrimSpace(strings.Trim(ref, "<>"))
	if !strings.HasPrefix(ref, "http://") && !strings.HasPrefix(ref, "https://") {
		return source.Attachment{}, errors.New("anexo GitHub precisa ser uma URL")
	}
	parsed, err := url.Parse(ref)
	if err != nil {
		return source.Attachment{}, err
	}
	if !allowedAttachmentHost(parsed.Hostname()) {
		return source.Attachment{}, fmt.Errorf("host de anexo não permitido: %s", parsed.Hostname())
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, ref, nil)
	if err != nil {
		return source.Attachment{}, err
	}
	request.Header.Set("Accept", "image/*")
	if token != "" && !s3AttachmentHost(parsed.Hostname()) {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			host := next.URL.Hostname()
			if !allowedAttachmentHost(host) {
				return fmt.Errorf("redirect de anexo para host não permitido: %s", host)
			}
			if token != "" && !s3AttachmentHost(host) {
				next.Header.Set("Authorization", "Bearer "+token)
			} else {
				next.Header.Del("Authorization")
			}
			return nil
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return source.Attachment{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return source.Attachment{}, fmt.Errorf("status HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxAttachmentBytes+1))
	if err != nil {
		return source.Attachment{}, err
	}
	if len(data) > maxAttachmentBytes {
		return source.Attachment{}, errors.New("anexo excede 10 MB")
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	if !strings.HasPrefix(contentType, "image/") {
		return source.Attachment{}, fmt.Errorf("conteúdo não é imagem: %s", contentType)
	}
	ext := attachmentExtension(parsed.Path, contentType)
	name := fmt.Sprintf("attachment-%02d%s", index, ext)
	path := filepath.Join(destDir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return source.Attachment{}, err
	}
	return source.Attachment{Name: name, URL: ref, Path: path, Size: int64(len(data))}, nil
}

func allowedAttachmentHost(host string) bool {
	host = strings.ToLower(host)
	switch host {
	case "github.com", "www.github.com", "raw.githubusercontent.com", "user-images.githubusercontent.com", "private-user-images.githubusercontent.com", "objects.githubusercontent.com":
		return true
	default:
		return s3AttachmentHost(host)
	}
}

func s3AttachmentHost(host string) bool {
	host = strings.ToLower(host)
	if !strings.HasSuffix(host, ".s3.amazonaws.com") {
		return false
	}
	return strings.HasPrefix(host, "github-production-user-asset-") ||
		strings.HasPrefix(host, "github-production-release-asset-")
}

func attachmentExtension(path, contentType string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return ".png"
	case ".jpg", ".jpeg":
		return ".jpg"
	case ".webp":
		return ".webp"
	case ".gif":
		return ".gif"
	}
	switch contentType {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".img"
	}
}
