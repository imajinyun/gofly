package generator

import (
	"testing"
)

// FuzzParseAPI ensures the .api IDL parser never panics on arbitrary input.
// Parse errors are expected and fine; a panic is a bug.
func FuzzParseAPI(f *testing.F) {
	f.Add("")
	f.Add("type User { ID int }")
	f.Add("service S {\n@handler h\nGET /u (User) returns (User)\n}")
	f.Add("type { }")
	f.Add("service")
	f.Add("type User {\n  ID int\n  Name string\n}\nservice US {\n  @handler getUser\n  GET /users/{id} (User) returns (User)\n}")

	f.Fuzz(func(t *testing.T, content string) {
		// Must not panic regardless of input. The returned error/value are
		// intentionally ignored — we only assert the parser is panic-free.
		_, _ = ParseAPI(content)
	})
}

// FuzzParseProto ensures the .proto IDL parser never panics on arbitrary input.
func FuzzParseProto(f *testing.F) {
	f.Add("")
	f.Add("syntax = \"proto3\";")
	f.Add("message User { int64 id = 1; }")
	f.Add("syntax = \"proto3\";\npackage demo;\nmessage User {\n  int64 id = 1;\n  string name = 2;\n}\nservice US {\n  rpc Get(User) returns (User);\n}")
	f.Add("message {")
	f.Add("service }")

	f.Fuzz(func(t *testing.T, content string) {
		_, _ = ParseProto(content)
	})
}
