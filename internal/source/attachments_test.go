package source

import "testing"

func TestMediaReferences(t *testing.T) {
	task := &Task{
		Description: "![Wireframe](https://github.com/user-attachments/assets/abc)\n<img src=\"docs/design/result.png\">\nVeja https://example.com/image.webp e https://example.com/page.",
		Comments:    []Comment{{Body: "![Detalhe](attachments/detalhe.jpg)"}},
	}
	refs := MediaReferences(task)
	if len(refs) != 4 {
		t.Fatalf("refs = %+v", refs)
	}
	if !MentionsVisualRequirement(task) {
		t.Fatal("wireframe não detectado")
	}
	if MentionsVisualRequirement(&Task{Description: "Apenas texto."}) {
		t.Fatal("falso positivo de requisito visual")
	}
}
