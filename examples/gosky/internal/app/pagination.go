package app

import "math"

// SmartAdmin page parameters are 1-based pageNum plus pageSize. These defaults
// and limits are the single contract every compatibility list endpoint must
// use so the unchanged client's pagination bar behaves consistently.
const (
	defaultPageNum  uint64 = 1
	defaultPageSize uint64 = 20
	maxPageSize     uint64 = 100
)

// NormalizePage applies the SmartAdmin default/clamp rules. pageNum is 1-based;
// a zero value means the first page, pageSize zero means the default size.
func NormalizePage(pageNum, pageSize uint64) (uint64, uint64) {
	if pageNum == 0 {
		pageNum = defaultPageNum
	}
	if pageSize == 0 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	return pageNum, pageSize
}

// PageBounds returns the [start,end) indexes for total items, clamped to an
// out-of-range page. Overflow on the page*size multiplication is bounded
// before multiplying so huge or wrapped uint64 inputs produce an empty page
// rather than an inverted slice.
func PageBounds(total int, pageNum, pageSize uint64) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	pageNum, pageSize = NormalizePage(pageNum, pageSize)
	if pageNum > uint64(total-1)/pageSize+1 {
		return total, total
	}
	start := (pageNum - 1) * pageSize
	end := start + pageSize
	if start > math.MaxInt || end > math.MaxInt {
		return total, total
	}
	if end > uint64(total) {
		end = uint64(total)
	}
	return int(start), int(end)
}

// Paginate returns the bounded page of items without copying the backing
// array; callers must not mutate the returned slice.
func Paginate[T any](items []T, pageNum, pageSize uint64) []T {
	start, end := PageBounds(len(items), pageNum, pageSize)
	return items[start:end]
}
