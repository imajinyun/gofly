// Package smartoperatelog exposes the unmodified SmartAdmin operation-audit
// contracts and provides the best-effort recorder middleware that writes
// audited compatibility mutations to support_operate_logs.
package smartoperatelog

// operateCatalog maps audited compatibility routes to their SmartAdmin
// module/content labels, mirroring the Java @Tag/@Operation annotations the
// aspect reads. Only mutations listed here are recorded; read queries and
// the login boundary are covered by their own login-log trail.
var operateCatalog = map[string]string{
	// roles and menus
	"/role/add":                              "角色管理|新增角色",
	"/role/update":                           "角色管理|编辑角色",
	"/role/delete/{id}":                      "角色管理|删除角色",
	"/role/dataScope/updateRoleDataScope":    "角色管理|更新数据范围",
	"/role/employee/batchAddRoleEmployee":    "角色管理|批量分配员工",
	"/role/employee/batchRemoveRoleEmployee": "角色管理|批量移除员工",
	"/role/menu/updateRoleMenu":              "角色管理|更新角色菜单",
	"/menu/add":                              "菜单管理|新增菜单",
	"/menu/update":                           "菜单管理|编辑菜单",
	"/menu/batchDelete/{ids}":                "菜单管理|批量删除菜单",

	// organization
	"/employee/add":                              "员工管理|新增员工",
	"/employee/update":                           "员工管理|编辑员工",
	"/employee/batchDelete":                      "员工管理|批量删除员工",
	"/employee/resetPassword":                    "员工管理|重置密码",
	"/employee/update/password":                  "个人中心|修改密码",
	"/employee/department/add":                   "部门管理|新增部门",
	"/employee/department/update":                "部门管理|编辑部门",
	"/employee/department/delete/{departmentId}": "部门管理|删除部门",
	"/employee/position/add":                     "职务管理|新增职务",
	"/employee/position/update":                  "职务管理|编辑职务",
	"/employee/position/delete/{positionId}":     "职务管理|删除职务",

	// dictionary
	"/support/dict/add":                                  "数据字典|新增字典",
	"/support/dict/update":                               "数据字典|编辑字典",
	"/support/dict/batchDelete":                          "数据字典|批量删除字典",
	"/support/dict/updateDisabled/{dictId}":              "数据字典|启停用字典",
	"/support/dict/dictData/add":                         "字典数据|新增字典项",
	"/support/dict/dictData/update":                      "字典数据|编辑字典项",
	"/support/dict/dictData/batchDelete":                 "字典数据|批量删除字典项",
	"/support/dict/dictData/updateDisabled/{dictDataId}": "字典数据|启停用字典项",

	// configuration
	"/support/config/add":    "参数配置|新增参数",
	"/support/config/update": "参数配置|编辑参数",

	// serial numbers
	"/support/serialNumber/generate": "单据序列号|生成单据号",

	// messages
	"/message/sendMessages":       "站内信|发送消息",
	"/message/delete/{messageId}": "站内信|删除消息",

	// feedback
	"/support/feedback/add": "意见反馈|提交反馈",

	// help documents
	"/support/helpDoc/helpDocCatalog/add":                       "帮助文档|新增文档目录",
	"/support/helpDoc/helpDocCatalog/update":                    "帮助文档|编辑文档目录",
	"/support/helpDoc/helpDocCatalog/delete/{helpDocCatalogId}": "帮助文档|删除文档目录",
	"/support/helpDoc/add":                                      "帮助文档|新增帮助文档",
	"/support/helpDoc/update":                                   "帮助文档|编辑帮助文档",
	"/support/helpDoc/delete/{helpDocId}":                       "帮助文档|删除帮助文档",

	// login protection
	"/support/protect/loginFail/batchDelete":      "登录保护|删除失败锁定",
	"/support/protect/level3protect/updateConfig": "三级等保|更新等保配置",
}

// catalogLabel returns "module|content" for a route, or empty when the route
// is not audited.
func catalogLabel(route string) (module, content string, audited bool) {
	label, ok := operateCatalog[route]
	if !ok {
		return "", "", false
	}
	if separator := indexByte(label, '|'); separator >= 0 {
		return label[:separator], label[separator+1:], true
	}
	return label, "", true
}

func indexByte(value string, b byte) int {
	for i := 0; i < len(value); i++ {
		if value[i] == b {
			return i
		}
	}
	return -1
}
