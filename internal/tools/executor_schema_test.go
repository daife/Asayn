package tools

import (
	"strings"
	"testing"
)

func TestFileEditSchemaExposesBatchItems(t *testing.T) {
	e := &Executor{}
	var params map[string]any
	for _, schema := range e.Schemas(false) {
		if schema.Function.Name == "file_edit" {
			params = schema.Function.Parameters
			break
		}
	}
	if params == nil {
		t.Fatal("file_edit schema not found")
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("file_edit schema missing properties")
	}
	mode, ok := props["mode"].(map[string]any)
	if !ok {
		t.Fatal("file_edit schema missing mode property")
	}
	if desc, _ := mode["description"].(string); desc == "" || !strings.Contains(desc, "batch") {
		t.Fatalf("mode description does not expose batch: %q", desc)
	}
	batch, ok := props["batch"].(map[string]any)
	if !ok {
		t.Fatal("file_edit schema missing batch property")
	}
	items, ok := batch["items"].(map[string]any)
	if !ok {
		t.Fatal("batch schema missing object items")
	}
	if typ, _ := items["type"].(string); typ != "object" {
		t.Fatalf("batch items type = %q, want object", typ)
	}
}
