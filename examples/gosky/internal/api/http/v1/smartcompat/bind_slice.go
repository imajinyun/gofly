package smartcompat

import (
	"encoding/json"
	"fmt"

	"github.com/imajinyun/gofly/rest"
)

// BindJSONSlice decodes a top-level JSON array request body.
//
// The framework's rest.BindJSON accepts only struct targets, while the
// unmodified SmartAdmin client sends batch operations (ValidateList<T>) as
// raw JSON arrays. Compatibility handlers use this helper instead of
// ctx.Bind for every slice body.
func BindJSONSlice[T any](ctx *rest.Context) ([]T, error) {
	var target []T
	if ctx == nil || ctx.Request == nil || ctx.Request.Body == nil {
		return nil, fmt.Errorf("empty request body")
	}
	decoder := json.NewDecoder(ctx.Request.Body)
	if err := decoder.Decode(&target); err != nil {
		return nil, fmt.Errorf("decode json array body: %w", err)
	}
	return target, nil
}

// BindUint64Array decodes the common raw []uint64 batch payload.
func BindUint64Array(ctx *rest.Context) ([]uint64, error) {
	return BindJSONSlice[uint64](ctx)
}
