package system

import (
	"strconv"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/rest"
)

// pathID parses a bounded unsigned route identifier. It writes the canonical
// 400 response on failure so handlers can simply return.
func pathID(ctx *rest.Context) (uint64, bool) {
	id, err := strconv.ParseUint(ctx.PathValue("id"), 10, 64)
	if err != nil || id == 0 {
		ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "id must be a positive integer"))
		return 0, false
	}
	return id, true
}
