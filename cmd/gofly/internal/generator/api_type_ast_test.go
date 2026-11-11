package generator

import (
	"strings"
	"testing"
)

func TestAPITypeASTCaptureAndNormalize(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		messageName string
		fieldName   string
		fieldType   string
		aliasName   string
		aliasTarget string
		alias       bool
	}{
		{
			name:        "single sugar struct",
			content:     "type User {\n  ID string `json:\"id\"`\n}\n",
			messageName: "User",
			fieldName:   "ID",
			fieldType:   "string",
		},
		{
			name:        "grouped alias and sugar struct",
			content:     "type (\n  UserID int64\n  Raw = any\n  User {\n    ID UserID\n  }\n)\n",
			messageName: "User",
			fieldName:   "ID",
			fieldType:   "UserID",
			aliasName:   "Raw",
			aliasTarget: "any",
			alias:       true,
		},
		{
			name:        "struct alias",
			content:     "type User = struct {\n  ID string\n}\n",
			messageName: "User",
			fieldName:   "ID",
			fieldType:   "string",
		},
		{
			name:        "tag with comment and brace punctuation",
			content:     "type User {\n  URL string `json:\"url\" validate:\"regexp=^/{a}//$\"`\n}\n",
			messageName: "User",
			fieldName:   "URL",
			fieldType:   "string",
		},
		{
			name:        "multi-name field",
			content:     "type User {\n  A, B string `json:\"-\"`\n}\n",
			messageName: "User",
			fieldName:   "A",
			fieldType:   "string",
		},
		{
			name:        "inline struct",
			content:     "type User {\n  Meta struct {\n    Source string\n  } `json:\"meta\"`\n}\n",
			messageName: "User",
			fieldName:   "Meta",
			fieldType:   "struct{}",
		},
		{
			name:        "inline slice struct",
			content:     "type User {\n  Items []struct {\n    Name string\n  } `json:\"items\"`\n}\n",
			messageName: "User",
			fieldName:   "Items",
			fieldType:   "[]struct{}",
		},
		{
			name:        "inline map struct",
			content:     "type User {\n  Index map[string]struct {\n    Count int\n  } `json:\"index\"`\n}\n",
			messageName: "User",
			fieldName:   "Index",
			fieldType:   "map[string]struct{}",
		},
		{
			name:        "embedded pointer with tag",
			content:     "type User {\n  *Audit `json:\"audit\"`\n}\n",
			messageName: "User",
			fieldName:   "Audit",
			fieldType:   "*Audit",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			doc, err := ParseAPIFile(test.content, "types.api")
			if err != nil {
				t.Fatalf("ParseAPIFile() error = %v", err)
			}
			if test.messageName != "" {
				var found *IDLMessage
				for index := range doc.Messages {
					if exportName(doc.Messages[index].Name) == test.messageName {
						found = &doc.Messages[index]
						break
					}
				}
				if found == nil {
					t.Fatalf("missing message %s in %#v", test.messageName, doc.Messages)
				}
				var field *IDLField
				for index := range found.Fields {
					if exportName(found.Fields[index].Name) == test.fieldName {
						field = &found.Fields[index]
						break
					}
				}
				if field == nil {
					t.Fatalf("missing field %s in %#v", test.fieldName, found.Fields)
				}
				if field.Type != test.fieldType {
					t.Fatalf("field %s type = %q, want %q", test.fieldName, field.Type, test.fieldType)
				}
			}
			if test.aliasName != "" {
				var found *IDLTypeAlias
				for index := range doc.Aliases {
					if exportName(doc.Aliases[index].Name) == test.aliasName {
						found = &doc.Aliases[index]
						break
					}
				}
				if found == nil {
					t.Fatalf("missing alias %s in %#v", test.aliasName, doc.Aliases)
				}
				if found.Target != test.aliasTarget || found.Alias != test.alias {
					t.Fatalf("alias = %#v, want target %q alias %t", found, test.aliasTarget, test.alias)
				}
			}
		})
	}
}

func TestAPITypeASTLineMappingAfterBlockComment(t *testing.T) {
	content := "/* leading comment\n   spanning lines */\ntype User {\n  Broken func()\n}\n"
	_, err := ParseAPIFile(content, "types.api")
	if err == nil {
		t.Fatal("ParseAPIFile() succeeded, want unsupported func error")
	}
	if !strings.Contains(err.Error(), "types.api:4") {
		t.Fatalf("error = %q, want original types.api:4", err)
	}
	if !strings.Contains(err.Error(), "unsupported Go type in .api: func") {
		t.Fatalf("error = %q, want stable func error", err)
	}
}

func TestAPITypeASTRejectedConstructs(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "generics", content: "type Box[T any] struct {\n  Value T\n}\n", want: "generics are not supported"},
		{name: "non-empty interface", content: "type Reader interface {\n  Read()\n}\n", want: "non-empty interface types are not supported"},
		{name: "func", content: "type Handler func()\n", want: "unsupported Go type in .api: func"},
		{name: "chan", content: "type Stream chan int\n", want: "unsupported Go type in .api: chan"},
		{name: "unsafe pointer", content: "type Pointer unsafe.Pointer\n", want: "unsupported Go type in .api: unsafe.Pointer"},
		{name: "complex", content: "type Value complex128\n", want: "unsupported Go type in .api: complex"},
		{name: "package outside allowlist", content: "type Value json.RawMessage\n", want: "unsupported package-qualified type json.RawMessage"},
		{name: "multi-name shared wire tag", content: "type User {\n  A, B string `json:\"shared\"`\n}\n", want: "must not share tag json"},
		{name: "unclosed group", content: "type (\n  A any\n", want: "type block is not closed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseAPIFile(test.content, "types.api")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseAPIFile() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestAPITypeASTStripsLineDirectives(t *testing.T) {
	content := "type User {\n  //line /etc/passwd:1\n  ID string\n}\n"
	doc, err := ParseAPIFile(content, "types.api")
	if err != nil {
		t.Fatalf("ParseAPIFile() error = %v", err)
	}
	if len(doc.Messages) != 1 || len(doc.Messages[0].Fields) != 1 || doc.Messages[0].Fields[0].Name != "ID" {
		t.Fatalf("doc = %#v, want one ID field", doc.Messages)
	}
}
