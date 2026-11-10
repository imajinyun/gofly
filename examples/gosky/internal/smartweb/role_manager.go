package smartweb

import (
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

// roleEmployeePage is the only browser-specific part of role membership:
// convert domain users into the unchanged SmartAdmin employee representation.
// Canonical subjects remain inside the application and are not serialized.
func roleEmployeePage(page app.RoleMemberPage, role string) RoleEmployeePageVO {
	list := make([]RoleEmployeeVO, 0, len(page.Users))
	for _, user := range page.Users {
		// The directory returns active bindings only, so disabledFlag is always
		// false here; the column exists for the unchanged SmartAdmin table.
		item := RoleEmployeeVO{
			EmployeeID:     user.UserID,
			LoginName:      user.LoginName,
			ActualName:     user.DisplayName,
			Phone:          user.Phone,
			DepartmentName: user.DepartmentName,
			CreateTime:     user.CreatedAt,
			RoleIDList:     make([]uint64, 0, 1),
			RoleNameList:   make([]string, 0, 1),
		}
		if role != "" {
			if roleID := smartWebRoleIDForCode(role); roleID != 0 {
				item.RoleIDList = append(item.RoleIDList, roleID)
				item.RoleNameList = append(item.RoleNameList, smartWebRoleName(role))
			}
		}
		list = append(list, item)
	}
	return RoleEmployeePageVO{List: list, Total: page.Total}
}

func smartWebRoleIDForCode(role string) uint64 {
	switch role {
	case authz.RoleReader:
		return SmartWebRoleReaderID
	case authz.RoleWriter:
		return SmartWebRoleWriterID
	case authz.RoleAdmin:
		return SmartWebRoleAdminID
	default:
		return 0
	}
}

func smartWebRoleName(role string) string {
	switch role {
	case authz.RoleReader:
		return "Reader"
	case authz.RoleWriter:
		return "Writer"
	case authz.RoleAdmin:
		return "Administrator"
	default:
		return ""
	}
}
