// Conversion between the app-layer dynamic RBAC records and SmartAdmin's
// historical MenuVO/role identifiers. No policy rows, subjects, or raw
// capabilities beyond the deployment catalog cross this boundary.
package smartweb

import (
	"sort"

	"github.com/imajinyun/gofly/examples/gosky/internal/app"
)

func menuRecordToVO(menu app.MenuRecord) MenuVO {
	return MenuVO{
		MenuID:       menu.ID,
		ParentID:     menu.ParentID,
		MenuName:     menu.Name,
		MenuType:     menu.Type,
		Sort:         menu.Sort,
		Path:         menu.Path,
		Component:    menu.Component,
		WebPerms:     menu.WebPerms,
		APIPerms:     "",
		Icon:         menu.Icon,
		VisibleFlag:  menu.Visible,
		DisabledFlag: menu.Disabled,
		FrameFlag:    menu.FrameFlag,
		CacheFlag:    menu.CacheFlag,
		FrameURL:     menu.FrameURL,
		PermsType:    0,
	}
}

// menuRecordsToTree assembles the flat tenant catalog into the SmartAdmin
// parent/children JSON shape ordered by sort then ID.
func menuRecordsToTree(records []app.MenuRecord, onlyMenu bool) []MenuVO {
	children := make(map[uint64][]app.MenuRecord)
	roots := make([]app.MenuRecord, 0)
	for _, menu := range records {
		if onlyMenu && menu.Type == SmartWebMenuTypePoint {
			continue
		}
		if menu.ParentID == 0 {
			roots = append(roots, menu)
		} else {
			children[menu.ParentID] = append(children[menu.ParentID], menu)
		}
	}
	for key := range children {
		sort.Slice(children[key], func(i, j int) bool { return recordBefore(children[key][i], children[key][j]) })
	}
	sort.Slice(roots, func(i, j int) bool { return recordBefore(roots[i], roots[j]) })
	var build func(records []app.MenuRecord) []MenuVO
	build = func(nodes []app.MenuRecord) []MenuVO {
		result := make([]MenuVO, 0, len(nodes))
		for _, node := range nodes {
			vo := menuRecordToVO(node)
			if nested := build(children[node.ID]); len(nested) > 0 {
				vo.Children = nested
			}
			result = append(result, vo)
		}
		return result
	}
	return build(roots)
}

func menuRecordsFlat(records []app.MenuRecord) []MenuVO {
	sort.Slice(records, func(i, j int) bool { return recordBefore(records[i], records[j]) })
	result := make([]MenuVO, 0, len(records))
	for _, record := range records {
		result = append(result, menuRecordToVO(record))
	}
	return result
}

func recordBefore(a, b app.MenuRecord) bool {
	if a.Sort != b.Sort {
		return a.Sort < b.Sort
	}
	return a.ID < b.ID
}

// structuralRecordIDs returns catalog and page node IDs for role selection
// echo, matching the historical smartwebStructuralMenuIDs behavior.
func structuralRecordIDs(records []app.MenuRecord) []uint64 {
	ids := make([]uint64, 0)
	for _, menu := range records {
		if menu.Type != SmartWebMenuTypePoint {
			ids = append(ids, menu.ID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// roleRecordVO converts a dynamic tenant role. Fixed roles keep their
// deployment constants and are emitted separately by RoleDefinitions.
func roleRecordVO(role app.RoleRecord) RoleVO {
	return RoleVO{
		RoleID:   role.ID,
		RoleName: role.Name,
		RoleCode: role.Code,
		Remark:   role.Remark,
	}
}
