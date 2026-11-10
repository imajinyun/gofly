package app

import "testing"

// Failure-mode inventory for the shared compatibility pagination:
//
//  1. PAGE-01: zero parameters mean page 1 with the default size.
//  2. PAGE-02: oversized pageSize is clamped to 100.
//  3. PAGE-03: a page past the end is empty, not wrapping.
//  4. PAGE-04: near-MaxUint64 page/size never invert the slice or panic.
//  5. PAGE-05: the last partial page returns only remaining items.
func TestNormalizePage(t *testing.T) {
	tests := []struct {
		name                       string
		pageNum, pageSize, wantNum uint64
		wantSize                   uint64
	}{
		{name: "zero defaults", pageNum: 0, pageSize: 0, wantNum: 1, wantSize: 20},
		{name: "page size clamp", pageNum: 1, pageSize: 500, wantNum: 1, wantSize: 100},
		{name: "values preserved", pageNum: 3, pageSize: 15, wantNum: 3, wantSize: 15},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotNum, gotSize := NormalizePage(tt.pageNum, tt.pageSize)
			if gotNum != tt.wantNum || gotSize != tt.wantSize {
				t.Fatalf("NormalizePage(%d,%d) = (%d,%d), want (%d,%d)", tt.pageNum, tt.pageSize, gotNum, gotSize, tt.wantNum, tt.wantSize)
			}
		})
	}
}

func TestPaginate(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}

	if got := Paginate(items, 0, 0); len(got) != 5 {
		t.Fatalf("default page = %v, want all 5 items", got)
	}
	if got := Paginate(items, 2, 2); len(got) != 2 || got[0] != 3 || got[1] != 4 {
		t.Fatalf("second page = %v, want [3 4]", got)
	}
	if got := Paginate(items, 3, 2); len(got) != 1 || got[0] != 5 {
		t.Fatalf("partial last page = %v, want [5]", got)
	}
	if got := Paginate(items, 9, 2); len(got) != 0 {
		t.Fatalf("page past end = %v, want empty page", got)
	}
	if got := Paginate(items, ^uint64(0), ^uint64(0)); len(got) != 0 {
		t.Fatalf("overflow inputs = %v, want empty page", got)
	}
	if got := Paginate(items, 1, 100); len(got) != 5 {
		t.Fatalf("clamped size page = %v, want all items", got)
	}
	if got := Paginate([]int{}, 1, 20); len(got) != 0 {
		t.Fatalf("empty set page = %v, want empty page", got)
	}
}
