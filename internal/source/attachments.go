package source

import (
	"regexp"
	"strings"
)

var (
	markdownImagePattern = regexp.MustCompile(`!\[[^\]]*\]\(([^)\s]+)`)
	htmlImagePattern     = regexp.MustCompile(`(?i)<img[^>]+src=["']([^"']+)`)
	rawImageURLPattern   = regexp.MustCompile(`https?://[^\s)>"']+`)
)

func MediaReferences(task *Task) []string {
	if task == nil {
		return nil
	}
	var text strings.Builder
	text.WriteString(task.Description)
	text.WriteString("\n")
	for _, comment := range task.Comments {
		text.WriteString(comment.Body)
		text.WriteString("\n")
	}
	value := text.String()
	seen := map[string]bool{}
	var refs []string
	add := func(ref string) {
		ref = strings.TrimSpace(strings.Trim(ref, "<>"))
		if ref == "" || seen[ref] {
			return
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	for _, match := range markdownImagePattern.FindAllStringSubmatch(value, -1) {
		if len(match) == 2 {
			add(match[1])
		}
	}
	for _, match := range htmlImagePattern.FindAllStringSubmatch(value, -1) {
		if len(match) == 2 {
			add(match[1])
		}
	}
	for _, match := range rawImageURLPattern.FindAllString(value, -1) {
		lower := strings.ToLower(match)
		if strings.Contains(lower, "user-attachments") || strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".jpeg") || strings.HasSuffix(lower, ".webp") || strings.HasSuffix(lower, ".gif") {
			add(match)
		}
	}
	return refs
}

func MentionsVisualRequirement(task *Task) bool {
	if task == nil {
		return false
	}
	text := strings.ToLower(task.Description)
	for _, comment := range task.Comments {
		text += "\n" + strings.ToLower(comment.Body)
	}
	for _, phrase := range []string{"wireframe", "conforme desenho", "conforme o desenho", "referência visual", "referencia visual", "screenshot", "captura de tela"} {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}
