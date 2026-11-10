package smartweb

import "github.com/imajinyun/gofly/examples/gosky/internal/authz"

// These IDs belong only to the current operator's SmartAdmin button
// projection. They must never be accepted as target-role capability IDs.
const (
	roleMenuUpdateButtonID       uint64 = 810000021
	roleDataScopeUpdateButtonID  uint64 = 810000022
	roleEmployeeAddButtonID      uint64 = 810000023
	roleEmployeeRemoveButtonID   uint64 = 810000024
	roleEmployeeBatchButtonID    uint64 = 810000025
	roleAddButtonID              uint64 = 810000026
	roleUpdateButtonID           uint64 = 810000027
	roleDeleteButtonID           uint64 = 810000028
	menuAddButtonID              uint64 = 810000041
	menuUpdateButtonID           uint64 = 810000042
	menuDeleteButtonID           uint64 = 810000043
	departmentQueryButtonID      uint64 = 810000051
	departmentAddButtonID        uint64 = 810000052
	departmentUpdateButtonID     uint64 = 810000053
	departmentDeleteButtonID     uint64 = 810000054
	dictAddButtonID              uint64 = 810000083
	dictUpdateButtonID           uint64 = 810000084
	dictDeleteButtonID           uint64 = 810000085
	dictDataAddButtonID          uint64 = 810000086
	dictDataUpdateButtonID       uint64 = 810000087
	dictDataDeleteButtonID       uint64 = 810000088
	configQueryButtonID          uint64 = 810000093
	configAddButtonID            uint64 = 810000094
	configUpdateButtonID         uint64 = 810000095
	serialRecordButtonID         uint64 = 810000103
	serialGenerateButtonID       uint64 = 810000104
	messageSendButtonID          uint64 = 810000109
	messageDeleteButtonID        uint64 = 810000110
	feedbackQueryButtonID        uint64 = 810000113
	helpDocQueryButtonID         uint64 = 810000117
	helpDocAddButtonID           uint64 = 810000118
	helpDocUpdateButtonID        uint64 = 810000119
	helpDocDeleteButtonID        uint64 = 810000120
	helpDocCatalogAddButtonID    uint64 = 810000121
	helpDocCatalogEditButtonID   uint64 = 810000122
	helpDocCatalogDeleteButtonID uint64 = 810000123
	loginLogQueryButtonID        uint64 = 810000129
	operateLogQueryButtonID      uint64 = 810000132
	operateLogDetailButtonID     uint64 = 810000133
	cacheKeysButtonID            uint64 = 810000141
	cacheClearButtonID           uint64 = 810000142
	reloadExecuteButtonID        uint64 = 810000143
	reloadResultButtonID         uint64 = 810000144
	jobQueryButtonID             uint64 = 810000149
	jobAddButtonID               uint64 = 810000150
	jobUpdateButtonID            uint64 = 810000151
	jobExecuteButtonID           uint64 = 810000152
	jobLogQueryButtonID          uint64 = 810000153
	jobDeleteButtonID            uint64 = 810000154
	changeLogQueryButtonID       uint64 = 810000164
	changeLogAddButtonID         uint64 = 810000165
	changeLogUpdateButtonID      uint64 = 810000166
	changeLogDeleteButtonID      uint64 = 810000167
	changeLogBatchDeleteButtonID uint64 = 810000168
	noticeQueryButtonID          uint64 = 810000173
	noticeAddButtonID            uint64 = 810000174
	noticeUpdateButtonID         uint64 = 810000175
	noticeDeleteButtonID         uint64 = 810000176
	noticeTypeQueryButtonID      uint64 = 810000177
	noticeTypeManageButtonID     uint64 = 810000178
	employeeAddButtonID          uint64 = 810000061
	employeeUpdateButtonID       uint64 = 810000062
	employeeDeleteButtonID       uint64 = 810000063
	employeeDepartmentButtonID   uint64 = 810000064
	employeeDisabledButtonID     uint64 = 810000065
	employeeResetPwdButtonID     uint64 = 810000066
	fileQueryButtonID            uint64 = 810000073
)

// smartWebMenuPages is the single source for structural nodes shared by login,
// catalog, role-tree rendering, and role-save validation. Pages do not grant
// capabilities; access is derived from the verified operator's policy.
func smartWebMenuPages() []MenuVO {
	return []MenuVO{
		{MenuID: SmartWebRoleCatalogMenuID, MenuName: "Access Control", MenuType: SmartWebMenuTypeCatalog, Sort: 200, VisibleFlag: true, Icon: "SafetyCertificateOutlined"},
		{MenuID: SmartWebRolePageMenuID, ParentID: SmartWebRoleCatalogMenuID, MenuName: "Role Management", MenuType: SmartWebMenuTypePage, Sort: 201, Path: "/system/role", Component: "system/role/index.vue", VisibleFlag: true, Icon: "SafetyCertificateOutlined"},
		{MenuID: SmartWebMenuCatalogPageMenuID, ParentID: SmartWebRoleCatalogMenuID, MenuName: "Menu Catalog", MenuType: SmartWebMenuTypePage, Sort: 202, Path: "/system/menu", Component: "system/menu/menu-list.vue", VisibleFlag: true, Icon: "UnorderedListOutlined"},
		{MenuID: SmartWebOrgCatalogMenuID, MenuName: "Organization", MenuType: SmartWebMenuTypeCatalog, Sort: 300, VisibleFlag: true, Icon: "TeamOutlined"},
		{MenuID: SmartWebEmployeePageMenuID, ParentID: SmartWebOrgCatalogMenuID, MenuName: "Employee Management", MenuType: SmartWebMenuTypePage, Sort: 301, Path: "/system/employee", Component: "system/employee/index.vue", VisibleFlag: true, Icon: "TeamOutlined"},
		{MenuID: SmartWebDepartmentPageMenuID, ParentID: SmartWebOrgCatalogMenuID, MenuName: "Department Management", MenuType: SmartWebMenuTypePage, Sort: 302, Path: "/system/department", Component: "system/department/department-list.vue", VisibleFlag: true, Icon: "ApartmentOutlined"},
		{MenuID: SmartWebPositionPageMenuID, ParentID: SmartWebOrgCatalogMenuID, MenuName: "Position Management", MenuType: SmartWebMenuTypePage, Sort: 303, Path: "/system/position", Component: "system/position/position-list.vue", VisibleFlag: true, Icon: "IdcardOutlined"},
		{MenuID: SmartWebFilePageMenuID, MenuName: "Files", MenuType: SmartWebMenuTypePage, Sort: 401, Path: "/support/file", Component: "support/file/file-list.vue", VisibleFlag: true, Icon: "FolderOutlined"},
		{MenuID: SmartWebDictPageMenuID, MenuName: "Dictionary", MenuType: SmartWebMenuTypePage, Sort: 402, Path: "/support/dict", Component: "support/dict/index.vue", VisibleFlag: true, Icon: "BookOutlined"},
		{MenuID: SmartWebConfigPageMenuID, MenuName: "Configuration", MenuType: SmartWebMenuTypePage, Sort: 403, Path: "/support/config", Component: "support/config/config-list.vue", VisibleFlag: true, Icon: "SettingOutlined"},
		{MenuID: SmartWebSerialPageMenuID, MenuName: "Serial Numbers", MenuType: SmartWebMenuTypePage, Sort: 404, Path: "/support/serial-number", Component: "support/serial-number/serial-number-list.vue", VisibleFlag: true, Icon: "OrderedListOutlined"},
		{MenuID: SmartWebMessagePageMenuID, MenuName: "Messages", MenuType: SmartWebMenuTypePage, Sort: 405, Path: "/support/message", Component: "support/message/message-list.vue", VisibleFlag: true, Icon: "MessageOutlined"},
		{MenuID: SmartWebFeedbackPageMenuID, MenuName: "Feedback", MenuType: SmartWebMenuTypePage, Sort: 406, Path: "/support/feedback", Component: "support/feedback/feedback-list.vue", VisibleFlag: true, Icon: "CommentOutlined"},
		{MenuID: SmartWebHelpDocPageMenuID, MenuName: "Help Docs", MenuType: SmartWebMenuTypePage, Sort: 407, Path: "/support/help-doc", Component: "support/help-doc/management/help-doc-manage-list.vue", VisibleFlag: true, Icon: "QuestionCircleOutlined"},
		{MenuID: SmartWebLoginLogPageMenuID, MenuName: "Login Logs", MenuType: SmartWebMenuTypePage, Sort: 408, Path: "/support/login-log", Component: "support/login-log/login-log-list.vue", VisibleFlag: true, Icon: "HistoryOutlined"},
		{MenuID: SmartWebLoginFailPageMenuID, MenuName: "Login Fail Locks", MenuType: SmartWebMenuTypePage, Sort: 409, Path: "/support/login-fail", Component: "support/login-fail/login-fail-list.vue", VisibleFlag: true, Icon: "LockOutlined"},
		{MenuID: SmartWebLevel3PageMenuID, MenuName: "Level-3 Protection", MenuType: SmartWebMenuTypePage, Sort: 410, Path: "/support/level3protect", Component: "support/level3protect/level3-protect-config-index.vue", VisibleFlag: true, Icon: "SafetyCertificateOutlined"},
		{MenuID: SmartWebOperateLogPageMenuID, MenuName: "Operate Logs", MenuType: SmartWebMenuTypePage, Sort: 411, Path: "/support/operate-log", Component: "support/operate-log/operate-log-list.vue", VisibleFlag: true, Icon: "ProfileOutlined"},
		{MenuID: SmartWebCachePageMenuID, MenuName: "Caches", MenuType: SmartWebMenuTypePage, Sort: 412, Path: "/support/cache", Component: "support/cache/cache-list.vue", VisibleFlag: true, Icon: "AppstoreOutlined"},
		{MenuID: SmartWebReloadPageMenuID, MenuName: "Smart Reload", MenuType: SmartWebMenuTypePage, Sort: 413, Path: "/support/reload", Component: "support/reload/reload-list.vue", VisibleFlag: true, Icon: "ReloadOutlined"},
		{MenuID: SmartWebJobPageMenuID, MenuName: "Scheduled Jobs", MenuType: SmartWebMenuTypePage, Sort: 414, Path: "/support/job", Component: "support/job/job-list.vue", VisibleFlag: true, Icon: "FieldTimeOutlined"},
		{MenuID: SmartWebEncryptPageMenuID, MenuName: "API Encrypt Demo", MenuType: SmartWebMenuTypePage, Sort: 415, Path: "/support/api-encrypt", Component: "support/api-encrypt/api-encrypt-index.vue", VisibleFlag: true, Icon: "SafetyOutlined"},
		{MenuID: SmartWebHeartbeatPageMenuID, MenuName: "Heart Beat", MenuType: SmartWebMenuTypePage, Sort: 416, Path: "/support/heart-beat", Component: "support/heart-beat/heart-beat-list.vue", VisibleFlag: true, Icon: "MonitorOutlined"},
		{MenuID: SmartWebChangeLogPageMenuID, MenuName: "Change Log", MenuType: SmartWebMenuTypePage, Sort: 417, Path: "/support/change-log", Component: "support/change-log/change-log-list.vue", VisibleFlag: true, Icon: "ContainerOutlined"},
		{MenuID: SmartWebCodeGenPageMenuID, MenuName: "Code Generator", MenuType: SmartWebMenuTypePage, Sort: 418, Path: "/support/code-generator", Component: "support/code-generator/code-generator-list.vue", VisibleFlag: true, Icon: "CodeOutlined"},
		{MenuID: SmartWebNoticePageMenuID, MenuName: "Notice Board", MenuType: SmartWebMenuTypePage, Sort: 419, Path: "/oa/notice", Component: "business/oa/notice/notice-list.vue", VisibleFlag: true, Icon: "NotificationOutlined"},
	}
}

func smartWebStructuralMenuIDs() []uint64 {
	pages := smartWebMenuPages()
	ids := make([]uint64, 0, len(pages))
	for _, page := range pages {
		ids = append(ids, page.MenuID)
	}
	return ids
}

// smartWebManagementNavigation defines the SmartAdmin operator controls that
// have no backend page of their own. Each webPerm is filtered using its own
// explicit capability at projection time; assigning Project or Greeter
// capabilities never grants these controls.
func smartWebManagementNavigation() []MenuVO {
	return append(smartWebMenuPages(),
		MenuVO{MenuID: roleMenuUpdateButtonID, ParentID: SmartWebRolePageMenuID, MenuName: "Manage roles", MenuType: SmartWebMenuTypePoint, Sort: 203, VisibleFlag: true, WebPerms: "system:role:menu:update"},
		MenuVO{MenuID: roleDataScopeUpdateButtonID, ParentID: SmartWebRolePageMenuID, MenuName: "Manage Project data scopes", MenuType: SmartWebMenuTypePoint, Sort: 204, VisibleFlag: true, WebPerms: "system:role:dataScope:update"},
		MenuVO{MenuID: roleEmployeeAddButtonID, ParentID: SmartWebRolePageMenuID, MenuName: "Add role employees", MenuType: SmartWebMenuTypePoint, Sort: 205, VisibleFlag: true, WebPerms: "system:role:employee:add"},
		MenuVO{MenuID: roleEmployeeRemoveButtonID, ParentID: SmartWebRolePageMenuID, MenuName: "Remove role employees", MenuType: SmartWebMenuTypePoint, Sort: 206, VisibleFlag: true, WebPerms: "system:role:employee:delete"},
		MenuVO{MenuID: roleEmployeeBatchButtonID, ParentID: SmartWebRolePageMenuID, MenuName: "Batch remove role employees", MenuType: SmartWebMenuTypePoint, Sort: 207, VisibleFlag: true, WebPerms: "system:role:employee:batch:delete"},
		MenuVO{MenuID: roleAddButtonID, ParentID: SmartWebRolePageMenuID, MenuName: "Add role", MenuType: SmartWebMenuTypePoint, Sort: 208, VisibleFlag: true, WebPerms: "system:role:add"},
		MenuVO{MenuID: roleUpdateButtonID, ParentID: SmartWebRolePageMenuID, MenuName: "Edit role", MenuType: SmartWebMenuTypePoint, Sort: 209, VisibleFlag: true, WebPerms: "system:role:update"},
		MenuVO{MenuID: roleDeleteButtonID, ParentID: SmartWebRolePageMenuID, MenuName: "Delete role", MenuType: SmartWebMenuTypePoint, Sort: 210, VisibleFlag: true, WebPerms: "system:role:delete"},
		MenuVO{MenuID: menuAddButtonID, ParentID: SmartWebMenuCatalogPageMenuID, MenuName: "Add menu", MenuType: SmartWebMenuTypePoint, Sort: 301, VisibleFlag: true, WebPerms: "system:menu:add"},
		MenuVO{MenuID: menuUpdateButtonID, ParentID: SmartWebMenuCatalogPageMenuID, MenuName: "Edit menu", MenuType: SmartWebMenuTypePoint, Sort: 302, VisibleFlag: true, WebPerms: "system:menu:update"},
		MenuVO{MenuID: menuDeleteButtonID, ParentID: SmartWebMenuCatalogPageMenuID, MenuName: "Batch delete menus", MenuType: SmartWebMenuTypePoint, Sort: 303, VisibleFlag: true, WebPerms: "system:menu:batchDelete"},
		MenuVO{MenuID: employeeAddButtonID, ParentID: SmartWebEmployeePageMenuID, MenuName: "Add employee", MenuType: SmartWebMenuTypePoint, Sort: 311, VisibleFlag: true, WebPerms: "system:employee:add"},
		MenuVO{MenuID: employeeUpdateButtonID, ParentID: SmartWebEmployeePageMenuID, MenuName: "Edit employee", MenuType: SmartWebMenuTypePoint, Sort: 312, VisibleFlag: true, WebPerms: "system:employee:update"},
		MenuVO{MenuID: employeeDeleteButtonID, ParentID: SmartWebEmployeePageMenuID, MenuName: "Delete employees", MenuType: SmartWebMenuTypePoint, Sort: 313, VisibleFlag: true, WebPerms: "system:employee:delete"},
		MenuVO{MenuID: employeeDepartmentButtonID, ParentID: SmartWebEmployeePageMenuID, MenuName: "Move employee department", MenuType: SmartWebMenuTypePoint, Sort: 314, VisibleFlag: true, WebPerms: "system:employee:department:update"},
		MenuVO{MenuID: employeeDisabledButtonID, ParentID: SmartWebEmployeePageMenuID, MenuName: "Toggle employee state", MenuType: SmartWebMenuTypePoint, Sort: 315, VisibleFlag: true, WebPerms: "system:employee:disabled"},
		MenuVO{MenuID: employeeResetPwdButtonID, ParentID: SmartWebEmployeePageMenuID, MenuName: "Reset employee password", MenuType: SmartWebMenuTypePoint, Sort: 316, VisibleFlag: true, WebPerms: "system:employee:password:reset"},
		MenuVO{MenuID: departmentQueryButtonID, ParentID: SmartWebDepartmentPageMenuID, MenuName: "Query departments", MenuType: SmartWebMenuTypePoint, Sort: 321, VisibleFlag: true, WebPerms: "support:department:query"},
		MenuVO{MenuID: departmentAddButtonID, ParentID: SmartWebDepartmentPageMenuID, MenuName: "Add department", MenuType: SmartWebMenuTypePoint, Sort: 322, VisibleFlag: true, WebPerms: "system:department:add"},
		MenuVO{MenuID: departmentUpdateButtonID, ParentID: SmartWebDepartmentPageMenuID, MenuName: "Edit department", MenuType: SmartWebMenuTypePoint, Sort: 323, VisibleFlag: true, WebPerms: "system:department:update"},
		MenuVO{MenuID: departmentDeleteButtonID, ParentID: SmartWebDepartmentPageMenuID, MenuName: "Delete department", MenuType: SmartWebMenuTypePoint, Sort: 324, VisibleFlag: true, WebPerms: "system:department:delete"},
		MenuVO{MenuID: fileQueryButtonID, ParentID: SmartWebFilePageMenuID, MenuName: "Query files", MenuType: SmartWebMenuTypePoint, Sort: 413, VisibleFlag: true, WebPerms: "support:file:query"},
		MenuVO{MenuID: dictAddButtonID, ParentID: SmartWebDictPageMenuID, MenuName: "Add dictionary", MenuType: SmartWebMenuTypePoint, Sort: 423, VisibleFlag: true, WebPerms: "support:dict:add"},
		MenuVO{MenuID: dictUpdateButtonID, ParentID: SmartWebDictPageMenuID, MenuName: "Edit dictionary", MenuType: SmartWebMenuTypePoint, Sort: 424, VisibleFlag: true, WebPerms: "support:dict:update"},
		MenuVO{MenuID: dictDeleteButtonID, ParentID: SmartWebDictPageMenuID, MenuName: "Delete dictionary", MenuType: SmartWebMenuTypePoint, Sort: 425, VisibleFlag: true, WebPerms: "support:dict:delete"},
		MenuVO{MenuID: dictDataAddButtonID, ParentID: SmartWebDictPageMenuID, MenuName: "Add dictionary value", MenuType: SmartWebMenuTypePoint, Sort: 426, VisibleFlag: true, WebPerms: "support:dictData:add"},
		MenuVO{MenuID: dictDataUpdateButtonID, ParentID: SmartWebDictPageMenuID, MenuName: "Edit dictionary value", MenuType: SmartWebMenuTypePoint, Sort: 427, VisibleFlag: true, WebPerms: "support:dictData:update"},
		MenuVO{MenuID: dictDataDeleteButtonID, ParentID: SmartWebDictPageMenuID, MenuName: "Delete dictionary value", MenuType: SmartWebMenuTypePoint, Sort: 428, VisibleFlag: true, WebPerms: "support:dictData:delete"},
		MenuVO{MenuID: configQueryButtonID, ParentID: SmartWebConfigPageMenuID, MenuName: "Query configuration", MenuType: SmartWebMenuTypePoint, Sort: 431, VisibleFlag: true, WebPerms: "support:config:query"},
		MenuVO{MenuID: configAddButtonID, ParentID: SmartWebConfigPageMenuID, MenuName: "Add configuration", MenuType: SmartWebMenuTypePoint, Sort: 432, VisibleFlag: true, WebPerms: "support:config:add"},
		MenuVO{MenuID: configUpdateButtonID, ParentID: SmartWebConfigPageMenuID, MenuName: "Edit configuration", MenuType: SmartWebMenuTypePoint, Sort: 433, VisibleFlag: true, WebPerms: "support:config:update"},
		MenuVO{MenuID: serialRecordButtonID, ParentID: SmartWebSerialPageMenuID, MenuName: "View serial records", MenuType: SmartWebMenuTypePoint, Sort: 441, VisibleFlag: true, WebPerms: "support:serialNumber:record"},
		MenuVO{MenuID: serialGenerateButtonID, ParentID: SmartWebSerialPageMenuID, MenuName: "Generate serial number", MenuType: SmartWebMenuTypePoint, Sort: 442, VisibleFlag: true, WebPerms: "support:serialNumber:generate"},
		MenuVO{MenuID: messageSendButtonID, ParentID: SmartWebMessagePageMenuID, MenuName: "Send message", MenuType: SmartWebMenuTypePoint, Sort: 451, VisibleFlag: true, WebPerms: "support:message:send"},
		MenuVO{MenuID: messageDeleteButtonID, ParentID: SmartWebMessagePageMenuID, MenuName: "Delete message", MenuType: SmartWebMenuTypePoint, Sort: 452, VisibleFlag: true, WebPerms: "support:message:delete"},
		MenuVO{MenuID: feedbackQueryButtonID, ParentID: SmartWebFeedbackPageMenuID, MenuName: "Query feedback", MenuType: SmartWebMenuTypePoint, Sort: 461, VisibleFlag: true, WebPerms: "feedback:query"},
		MenuVO{MenuID: helpDocQueryButtonID, ParentID: SmartWebHelpDocPageMenuID, MenuName: "Query help docs", MenuType: SmartWebMenuTypePoint, Sort: 471, VisibleFlag: true, WebPerms: "support:helpDoc:query"},
		MenuVO{MenuID: helpDocAddButtonID, ParentID: SmartWebHelpDocPageMenuID, MenuName: "Add help doc", MenuType: SmartWebMenuTypePoint, Sort: 472, VisibleFlag: true, WebPerms: "support:helpDoc:add"},
		MenuVO{MenuID: helpDocUpdateButtonID, ParentID: SmartWebHelpDocPageMenuID, MenuName: "Edit help doc", MenuType: SmartWebMenuTypePoint, Sort: 473, VisibleFlag: true, WebPerms: "support:helpDoc:update"},
		MenuVO{MenuID: helpDocDeleteButtonID, ParentID: SmartWebHelpDocPageMenuID, MenuName: "Delete help doc", MenuType: SmartWebMenuTypePoint, Sort: 474, VisibleFlag: true, WebPerms: "support:helpDoc:delete"},
		MenuVO{MenuID: helpDocCatalogAddButtonID, ParentID: SmartWebHelpDocPageMenuID, MenuName: "Add help doc catalog", MenuType: SmartWebMenuTypePoint, Sort: 475, VisibleFlag: true, WebPerms: "support:helpDocCatalog:addCategory"},
		MenuVO{MenuID: helpDocCatalogEditButtonID, ParentID: SmartWebHelpDocPageMenuID, MenuName: "Edit help doc catalog", MenuType: SmartWebMenuTypePoint, Sort: 476, VisibleFlag: true, WebPerms: "support:helpDocCatalog:edit"},
		MenuVO{MenuID: helpDocCatalogDeleteButtonID, ParentID: SmartWebHelpDocPageMenuID, MenuName: "Delete help doc catalog", MenuType: SmartWebMenuTypePoint, Sort: 477, VisibleFlag: true, WebPerms: "support:helpDocCatalog:delete"},
		MenuVO{MenuID: loginLogQueryButtonID, ParentID: SmartWebLoginLogPageMenuID, MenuName: "Query login logs", MenuType: SmartWebMenuTypePoint, Sort: 481, VisibleFlag: true, WebPerms: "support:loginLog:query"},
		MenuVO{MenuID: operateLogQueryButtonID, ParentID: SmartWebOperateLogPageMenuID, MenuName: "Query operate logs", MenuType: SmartWebMenuTypePoint, Sort: 491, VisibleFlag: true, WebPerms: "support:operateLog:query"},
		MenuVO{MenuID: operateLogDetailButtonID, ParentID: SmartWebOperateLogPageMenuID, MenuName: "View operate log detail", MenuType: SmartWebMenuTypePoint, Sort: 492, VisibleFlag: true, WebPerms: "support:operateLog:detail"},
		MenuVO{MenuID: cacheKeysButtonID, ParentID: SmartWebCachePageMenuID, MenuName: "Inspect cache keys", MenuType: SmartWebMenuTypePoint, Sort: 501, VisibleFlag: true, WebPerms: "support:cache:keys"},
		MenuVO{MenuID: cacheClearButtonID, ParentID: SmartWebCachePageMenuID, MenuName: "Clear cache", MenuType: SmartWebMenuTypePoint, Sort: 502, VisibleFlag: true, WebPerms: "support:cache:delete"},
		MenuVO{MenuID: reloadExecuteButtonID, ParentID: SmartWebReloadPageMenuID, MenuName: "Execute reload", MenuType: SmartWebMenuTypePoint, Sort: 503, VisibleFlag: true, WebPerms: "support:reload:execute"},
		MenuVO{MenuID: reloadResultButtonID, ParentID: SmartWebReloadPageMenuID, MenuName: "View reload results", MenuType: SmartWebMenuTypePoint, Sort: 504, VisibleFlag: true, WebPerms: "support:reload:result"},
		MenuVO{MenuID: jobQueryButtonID, ParentID: SmartWebJobPageMenuID, MenuName: "Query jobs", MenuType: SmartWebMenuTypePoint, Sort: 511, VisibleFlag: true, WebPerms: "support:job:query"},
		MenuVO{MenuID: jobAddButtonID, ParentID: SmartWebJobPageMenuID, MenuName: "Add job", MenuType: SmartWebMenuTypePoint, Sort: 512, VisibleFlag: true, WebPerms: "support:job:add"},
		MenuVO{MenuID: jobUpdateButtonID, ParentID: SmartWebJobPageMenuID, MenuName: "Update job", MenuType: SmartWebMenuTypePoint, Sort: 513, VisibleFlag: true, WebPerms: "support:job:update"},
		MenuVO{MenuID: jobExecuteButtonID, ParentID: SmartWebJobPageMenuID, MenuName: "Execute job", MenuType: SmartWebMenuTypePoint, Sort: 514, VisibleFlag: true, WebPerms: "support:job:execute"},
		MenuVO{MenuID: jobLogQueryButtonID, ParentID: SmartWebJobPageMenuID, MenuName: "Query job logs", MenuType: SmartWebMenuTypePoint, Sort: 515, VisibleFlag: true, WebPerms: "support:job:log:query"},
		MenuVO{MenuID: jobDeleteButtonID, ParentID: SmartWebJobPageMenuID, MenuName: "Delete job", MenuType: SmartWebMenuTypePoint, Sort: 516, VisibleFlag: true, WebPerms: "support:job:log:delete"},
		MenuVO{MenuID: changeLogQueryButtonID, ParentID: SmartWebChangeLogPageMenuID, MenuName: "Query change logs", MenuType: SmartWebMenuTypePoint, Sort: 521, VisibleFlag: true, WebPerms: "support:changeLog:query"},
		MenuVO{MenuID: changeLogAddButtonID, ParentID: SmartWebChangeLogPageMenuID, MenuName: "Add change log", MenuType: SmartWebMenuTypePoint, Sort: 522, VisibleFlag: true, WebPerms: "support:changeLog:add"},
		MenuVO{MenuID: changeLogUpdateButtonID, ParentID: SmartWebChangeLogPageMenuID, MenuName: "Update change log", MenuType: SmartWebMenuTypePoint, Sort: 523, VisibleFlag: true, WebPerms: "support:changeLog:update"},
		MenuVO{MenuID: changeLogDeleteButtonID, ParentID: SmartWebChangeLogPageMenuID, MenuName: "Delete change log", MenuType: SmartWebMenuTypePoint, Sort: 524, VisibleFlag: true, WebPerms: "support:changeLog:delete"},
		MenuVO{MenuID: changeLogBatchDeleteButtonID, ParentID: SmartWebChangeLogPageMenuID, MenuName: "Batch delete change logs", MenuType: SmartWebMenuTypePoint, Sort: 525, VisibleFlag: true, WebPerms: "support:changeLog:batchDelete"},
		MenuVO{MenuID: noticeQueryButtonID, ParentID: SmartWebNoticePageMenuID, MenuName: "Query notices", MenuType: SmartWebMenuTypePoint, Sort: 531, VisibleFlag: true, WebPerms: "oa:notice:query"},
		MenuVO{MenuID: noticeAddButtonID, ParentID: SmartWebNoticePageMenuID, MenuName: "Add notice", MenuType: SmartWebMenuTypePoint, Sort: 532, VisibleFlag: true, WebPerms: "oa:notice:add"},
		MenuVO{MenuID: noticeUpdateButtonID, ParentID: SmartWebNoticePageMenuID, MenuName: "Update notice", MenuType: SmartWebMenuTypePoint, Sort: 533, VisibleFlag: true, WebPerms: "oa:notice:update"},
		MenuVO{MenuID: noticeDeleteButtonID, ParentID: SmartWebNoticePageMenuID, MenuName: "Delete notice", MenuType: SmartWebMenuTypePoint, Sort: 534, VisibleFlag: true, WebPerms: "oa:notice:delete"},
		MenuVO{MenuID: noticeTypeQueryButtonID, ParentID: SmartWebNoticePageMenuID, MenuName: "Query notice types", MenuType: SmartWebMenuTypePoint, Sort: 535, VisibleFlag: true, WebPerms: "oa:noticeType:query"},
		MenuVO{MenuID: noticeTypeManageButtonID, ParentID: SmartWebNoticePageMenuID, MenuName: "Manage notice types", MenuType: SmartWebMenuTypePoint, Sort: 536, VisibleFlag: true, WebPerms: "oa:noticeType:manage"},
	)
}

// managementPageCapabilities gates catalog/page visibility in the login
// navigation projection. Pages absent from the map (e.g. tenant-created
// pages) keep the directory-grant behavior instead of a hardcoded capability.
var managementPageCapabilities = map[uint64]authz.Capability{
	SmartWebRoleCatalogMenuID:     authz.CapabilityAuthorizationRoleRead,
	SmartWebRolePageMenuID:        authz.CapabilityAuthorizationRoleRead,
	SmartWebMenuCatalogPageMenuID: authz.CapabilityAuthorizationRoleRead,
	SmartWebOrgCatalogMenuID:      authz.CapabilitySystemUserManage,
	SmartWebEmployeePageMenuID:    authz.CapabilitySystemUserManage,
	SmartWebDepartmentPageMenuID:  authz.CapabilitySystemUserManage,
	SmartWebPositionPageMenuID:    authz.CapabilitySystemUserManage,
	SmartWebFilePageMenuID:        authz.CapabilitySupportFileRead,
	SmartWebDictPageMenuID:        authz.CapabilitySupportDictRead,
	SmartWebConfigPageMenuID:      authz.CapabilitySupportConfigRead,
	SmartWebSerialPageMenuID:      authz.CapabilitySupportSerialRead,
	SmartWebMessagePageMenuID:     authz.CapabilitySupportMessageRead,
	SmartWebFeedbackPageMenuID:    authz.CapabilitySupportFeedbackRead,
	SmartWebHelpDocPageMenuID:     authz.CapabilitySupportHelpDocRead,
	SmartWebLoginLogPageMenuID:    authz.CapabilitySupportLoginLogRead,
	SmartWebLoginFailPageMenuID:   authz.CapabilitySupportLoginLogManage,
	SmartWebLevel3PageMenuID:      authz.CapabilitySupportLoginLogManage,
	SmartWebOperateLogPageMenuID:  authz.CapabilitySupportOperateLogRead,
	SmartWebCachePageMenuID:       authz.CapabilitySupportCacheRead,
	SmartWebReloadPageMenuID:      authz.CapabilitySupportReloadRead,
	SmartWebJobPageMenuID:         authz.CapabilitySupportJobRead,
	SmartWebEncryptPageMenuID:     authz.CapabilitySupportDevToolsRead,
	SmartWebHeartbeatPageMenuID:   authz.CapabilitySupportDevToolsRead,
	SmartWebChangeLogPageMenuID:   authz.CapabilitySupportChangeLogRead,
	SmartWebCodeGenPageMenuID:     authz.CapabilitySupportDevToolsRead,
	SmartWebNoticePageMenuID:      authz.CapabilityOANoticeRead,
}

// smartWebCapabilityCatalog describes grants to a target role. Labels and
// values come from the same definitions used when saving capability IDs.
// It deliberately contains no current-operator management-button webPerms.
func smartWebCapabilityCatalog() []MenuVO {
	menus := smartWebMenuPages()
	for index, definition := range smartWebCapabilityMenus {
		menus = append(menus, MenuVO{
			MenuID: definition.menuID, ParentID: SmartWebRolePageMenuID,
			MenuName: definition.name, MenuType: SmartWebMenuTypePoint,
			Sort: 203 + index, VisibleFlag: true,
		})
	}
	return menus
}

func smartWebMenuTree(onlyMenu bool) []MenuVO {
	byParent := make(map[uint64][]MenuVO)
	for _, menu := range smartWebCapabilityCatalog() {
		if onlyMenu && menu.MenuType == SmartWebMenuTypePoint {
			continue
		}
		byParent[menu.ParentID] = append(byParent[menu.ParentID], menu)
	}
	var build func(uint64) []MenuVO
	build = func(parentID uint64) []MenuVO {
		children := byParent[parentID]
		for index := range children {
			children[index].Children = build(children[index].MenuID)
		}
		return children
	}
	return build(0)
}

// UI permissions map to independent backend capabilities, never a common bypass.
var managementButtonCapabilities = map[string]authz.Capability{
	"system:role:menu:update":            authz.CapabilityAuthorizationRoleManage,
	"system:role:dataScope:update":       authz.CapabilityAuthorizationRoleScopeManage,
	"system:role:employee:add":           authz.CapabilityAuthorizationRoleMemberAdd,
	"system:role:employee:delete":        authz.CapabilityAuthorizationRoleMemberRemove,
	"system:role:employee:batch:delete":  authz.CapabilityAuthorizationRoleMemberRemove,
	"system:role:add":                    authz.CapabilityAuthorizationRoleManage,
	"system:role:update":                 authz.CapabilityAuthorizationRoleManage,
	"system:role:delete":                 authz.CapabilityAuthorizationRoleManage,
	"system:menu:add":                    authz.CapabilitySystemMenuManage,
	"system:menu:update":                 authz.CapabilitySystemMenuManage,
	"system:menu:batchDelete":            authz.CapabilitySystemMenuManage,
	"system:employee:add":                authz.CapabilitySystemUserManage,
	"system:employee:update":             authz.CapabilitySystemUserManage,
	"system:employee:delete":             authz.CapabilitySystemUserManage,
	"system:employee:department:update":  authz.CapabilitySystemUserManage,
	"system:employee:disabled":           authz.CapabilitySystemUserManage,
	"system:employee:password:reset":     authz.CapabilitySystemUserManage,
	"support:department:query":           authz.CapabilitySystemUserManage,
	"system:department:add":              authz.CapabilitySystemUserManage,
	"system:department:update":           authz.CapabilitySystemUserManage,
	"system:department:delete":           authz.CapabilitySystemUserManage,
	"support:file:query":                 authz.CapabilitySupportFileRead,
	"support:dict:add":                   authz.CapabilitySupportDictManage,
	"support:dict:update":                authz.CapabilitySupportDictManage,
	"support:dict:delete":                authz.CapabilitySupportDictManage,
	"support:dictData:add":               authz.CapabilitySupportDictManage,
	"support:dictData:update":            authz.CapabilitySupportDictManage,
	"support:dictData:delete":            authz.CapabilitySupportDictManage,
	"support:config:query":               authz.CapabilitySupportConfigRead,
	"support:config:add":                 authz.CapabilitySupportConfigManage,
	"support:config:update":              authz.CapabilitySupportConfigManage,
	"support:serialNumber:record":        authz.CapabilitySupportSerialRead,
	"support:serialNumber:generate":      authz.CapabilitySupportSerialGenerate,
	"support:message:send":               authz.CapabilitySupportMessageSend,
	"support:message:delete":             authz.CapabilitySupportMessageSend,
	"feedback:query":                     authz.CapabilitySupportFeedbackRead,
	"support:helpDoc:query":              authz.CapabilitySupportHelpDocRead,
	"support:helpDoc:add":                authz.CapabilitySupportHelpDocManage,
	"support:helpDoc:update":             authz.CapabilitySupportHelpDocManage,
	"support:helpDoc:delete":             authz.CapabilitySupportHelpDocManage,
	"support:helpDocCatalog:addCategory": authz.CapabilitySupportHelpDocManage,
	"support:helpDocCatalog:edit":        authz.CapabilitySupportHelpDocManage,
	"support:helpDocCatalog:delete":      authz.CapabilitySupportHelpDocManage,
	"support:loginLog:query":             authz.CapabilitySupportLoginLogRead,
	"support:operateLog:query":           authz.CapabilitySupportOperateLogRead,
	"support:operateLog:detail":          authz.CapabilitySupportOperateLogRead,
	"support:cache:keys":                 authz.CapabilitySupportCacheRead,
	"support:cache:delete":               authz.CapabilitySupportCacheManage,
	"support:reload:execute":             authz.CapabilitySupportReloadManage,
	"support:reload:result":              authz.CapabilitySupportReloadRead,
	"support:job:query":                  authz.CapabilitySupportJobRead,
	"support:job:add":                    authz.CapabilitySupportJobManage,
	"support:job:update":                 authz.CapabilitySupportJobManage,
	"support:job:log:delete":             authz.CapabilitySupportJobManage,
	"support:job:execute":                authz.CapabilitySupportJobExecute,
	"support:job:log:query":              authz.CapabilitySupportJobRead,
	"support:changeLog:query":            authz.CapabilitySupportChangeLogRead,
	"support:changeLog:add":              authz.CapabilitySupportChangeLogManage,
	"support:changeLog:update":           authz.CapabilitySupportChangeLogManage,
	"support:changeLog:delete":           authz.CapabilitySupportChangeLogManage,
	"support:changeLog:batchDelete":      authz.CapabilitySupportChangeLogManage,
	"oa:notice:query":                    authz.CapabilityOANoticeRead,
	"oa:notice:add":                      authz.CapabilityOANoticeManage,
	"oa:notice:update":                   authz.CapabilityOANoticeManage,
	"oa:notice:delete":                   authz.CapabilityOANoticeManage,
	"oa:noticeType:query":                authz.CapabilityOANoticeRead,
	"oa:noticeType:manage":               authz.CapabilityOANoticeManage,
}
