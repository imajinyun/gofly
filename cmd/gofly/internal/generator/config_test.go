package generator

import "testing"

func TestCopySortedMapReturnsIndependentCopy(t *testing.T) {
	src := map[string]string{"b": "2", "a": "1"}
	got := copySortedMap(src)
	src["a"] = "changed"

	if got["a"] != "1" {
		t.Fatalf("copySortedMap reused source map, got a=%q", got["a"])
	}
	if got["b"] != "2" {
		t.Fatalf("copySortedMap missing b, got %q", got["b"])
	}
	if len(got) != 2 {
		t.Fatalf("copySortedMap len = %d, want 2", len(got))
	}
}

func TestCopySortedMapHandlesNilInput(t *testing.T) {
	got := copySortedMap(nil)
	if got == nil {
		t.Fatal("copySortedMap(nil) returned nil map, want writable empty map")
	}
	got["key"] = "value"
	if got["key"] != "value" {
		t.Fatalf("copySortedMap(nil) returned non-writable map: %#v", got)
	}
}
