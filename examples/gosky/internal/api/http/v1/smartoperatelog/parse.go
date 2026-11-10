package smartoperatelog

import (
	"strconv"
	"strings"
)

func parseOperateID(value string) (uint64, bool) {
	id, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	return id, err == nil && id != 0
}
