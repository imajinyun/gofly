// Deployment-owned seed catalog for dynamic RBAC. The IDs, names, and
// capabilities here mirror migration 20261014120000_dynamic_rbac.up.sql; a
// repository test asserts the two stay in sync.
package repo

import "github.com/imajinyun/gofly/examples/gosky/internal/authz"

// Custom menu IDs allocated per tenant start above every seeded ID so they can
// never collide with the SmartAdmin hardcoded 810000xxx range.
const customMenuIDStart = uint64(820000000)

type seedRole struct {
	code   string
	name   string
	remark string
}

var seedRoles = []seedRole{
	{code: authz.RoleReader, name: "Reader", remark: "Read-only tenant role"},
	{code: authz.RoleWriter, name: "Writer", remark: "Writer inherits reader"},
	{code: authz.RoleAdmin, name: "Administrator", remark: "Administrator inherits writer"},
}

type seedMenu struct {
	id         uint64
	parentID   uint64
	name       string
	typ        int
	sort       int
	path       string
	component  string
	webPerms   string
	capability string
	icon       string
	visible    bool
}

// seedMenus lists structural nodes, capability points, and operator buttons
// in stable ID order. Operator buttons carry webPerms only and can never be
// saved as target-role grants.
var seedMenus = []seedMenu{
	{id: 810000001, name: "Access Control", typ: 1, sort: 200, icon: "SafetyCertificateOutlined", visible: true},
	{id: 810000002, parentID: 810000001, name: "Role Management", typ: 2, sort: 201, path: "/system/role", component: "system/role/index.vue", icon: "SafetyCertificateOutlined", visible: true},
	{id: 810000003, parentID: 810000001, name: "Menu Catalog", typ: 2, sort: 202, path: "/system/menu", component: "system/menu/menu-list.vue", icon: "UnorderedListOutlined", visible: true},
	{id: 810000004, name: "Organization", typ: 1, sort: 300, icon: "TeamOutlined", visible: true},
	{id: 810000005, parentID: 810000004, name: "Employee Management", typ: 2, sort: 301, path: "/system/employee", component: "system/employee/index.vue", icon: "TeamOutlined", visible: true},
	{id: 810000006, parentID: 810000004, name: "Department Management", typ: 2, sort: 302, path: "/system/department", component: "system/department/department-list.vue", icon: "ApartmentOutlined", visible: true},
	{id: 810000007, parentID: 810000004, name: "Position Management", typ: 2, sort: 303, path: "/system/position", component: "system/position/position-list.vue", icon: "IdcardOutlined", visible: true},

	{id: 810000017, parentID: 810000002, name: "Update related Projects", typ: 3, sort: 217, capability: string(authz.CapabilityProjectUpdate), visible: true},
	{id: 810000018, parentID: 810000002, name: "Delete owned Projects", typ: 3, sort: 218, capability: string(authz.CapabilityProjectDelete), visible: true},
	{id: 810000019, parentID: 810000002, name: "Manage all tenant Projects (scope only)", typ: 3, sort: 219, capability: string(authz.CapabilityProjectManageAny), visible: true},
	{id: 810000031, parentID: 810000002, name: "View tenant roles and employees", typ: 3, sort: 231, capability: string(authz.CapabilityAuthorizationRoleRead), visible: true},
	{id: 810000032, parentID: 810000002, name: "Edit Project read scope", typ: 3, sort: 232, capability: string(authz.CapabilityAuthorizationRoleScopeManage), visible: true},
	{id: 810000033, parentID: 810000002, name: "Assign tenant roles", typ: 3, sort: 233, capability: string(authz.CapabilityAuthorizationRoleMemberAdd), visible: true},
	{id: 810000034, parentID: 810000002, name: "Remove tenant roles", typ: 3, sort: 234, capability: string(authz.CapabilityAuthorizationRoleMemberRemove), visible: true},
	{id: 810000035, parentID: 810000002, name: "Read policy audit events", typ: 3, sort: 235, capability: string(authz.CapabilityAuthorizationPolicyEventRead), visible: true},
	{id: 810000036, parentID: 810000002, name: "Manage tenant users", typ: 3, sort: 236, capability: string(authz.CapabilitySystemUserManage), visible: true},
	{id: 810000037, parentID: 810000002, name: "Manage tenant menus", typ: 3, sort: 237, capability: string(authz.CapabilitySystemMenuManage), visible: true},

	{id: 810000011, parentID: 810000002, name: "Read related Projects", typ: 3, sort: 211, capability: string(authz.CapabilityProjectRead), visible: true},
	{id: 810000012, parentID: 810000002, name: "Read all tenant Projects", typ: 3, sort: 212, capability: string(authz.CapabilityProjectReadAny), visible: true},
	{id: 810000013, parentID: 810000002, name: "Manage Project collaborators", typ: 3, sort: 213, capability: string(authz.CapabilityProjectMemberManage), visible: true},
	{id: 810000014, parentID: 810000002, name: "Edit role permissions", typ: 3, sort: 214, capability: string(authz.CapabilityAuthorizationRoleManage), visible: true},
	{id: 810000015, parentID: 810000002, name: "Call Greeter", typ: 3, sort: 215, capability: string(authz.CapabilityGreeterHello), visible: true},
	{id: 810000016, parentID: 810000002, name: "Watch Greeter stream", typ: 3, sort: 216, capability: string(authz.CapabilityGreeterWatch), visible: true},

	{id: 810000021, parentID: 810000002, name: "Manage roles", typ: 3, sort: 203, webPerms: "system:role:menu:update", visible: true},
	{id: 810000022, parentID: 810000002, name: "Manage Project data scopes", typ: 3, sort: 204, webPerms: "system:role:dataScope:update", visible: true},
	{id: 810000023, parentID: 810000002, name: "Add role employees", typ: 3, sort: 205, webPerms: "system:role:employee:add", visible: true},
	{id: 810000024, parentID: 810000002, name: "Remove role employees", typ: 3, sort: 206, webPerms: "system:role:employee:delete", visible: true},
	{id: 810000025, parentID: 810000002, name: "Batch remove role employees", typ: 3, sort: 207, webPerms: "system:role:employee:batch:delete", visible: true},
	{id: 810000026, parentID: 810000002, name: "Add role", typ: 3, sort: 208, webPerms: "system:role:add", visible: true},
	{id: 810000027, parentID: 810000002, name: "Edit role", typ: 3, sort: 209, webPerms: "system:role:update", visible: true},
	{id: 810000028, parentID: 810000002, name: "Delete role", typ: 3, sort: 210, webPerms: "system:role:delete", visible: true},
	{id: 810000041, parentID: 810000003, name: "Add menu", typ: 3, sort: 301, webPerms: "system:menu:add", visible: true},
	{id: 810000042, parentID: 810000003, name: "Edit menu", typ: 3, sort: 302, webPerms: "system:menu:update", visible: true},
	{id: 810000043, parentID: 810000003, name: "Batch delete menus", typ: 3, sort: 303, webPerms: "system:menu:batchDelete", visible: true},
	{id: 810000061, parentID: 810000005, name: "Add employee", typ: 3, sort: 311, webPerms: "system:employee:add", visible: true},
	{id: 810000062, parentID: 810000005, name: "Edit employee", typ: 3, sort: 312, webPerms: "system:employee:update", visible: true},
	{id: 810000063, parentID: 810000005, name: "Delete employees", typ: 3, sort: 313, webPerms: "system:employee:delete", visible: true},
	{id: 810000064, parentID: 810000005, name: "Move employee department", typ: 3, sort: 314, webPerms: "system:employee:department:update", visible: true},
	{id: 810000065, parentID: 810000005, name: "Toggle employee state", typ: 3, sort: 315, webPerms: "system:employee:disabled", visible: true},
	{id: 810000066, parentID: 810000005, name: "Reset employee password", typ: 3, sort: 316, webPerms: "system:employee:password:reset", visible: true},
	{id: 810000051, parentID: 810000006, name: "Query departments", typ: 3, sort: 321, webPerms: "support:department:query", visible: true},
	{id: 810000052, parentID: 810000006, name: "Add department", typ: 3, sort: 322, webPerms: "system:department:add", visible: true},
	{id: 810000053, parentID: 810000006, name: "Edit department", typ: 3, sort: 323, webPerms: "system:department:update", visible: true},
	{id: 810000054, parentID: 810000006, name: "Delete department", typ: 3, sort: 324, webPerms: "system:department:delete", visible: true},

	{id: 810000071, parentID: 810000008, name: "View files", typ: 3, sort: 411, capability: string(authz.CapabilitySupportFileRead), visible: true},
	{id: 810000072, parentID: 810000008, name: "Upload and manage files", typ: 3, sort: 412, capability: string(authz.CapabilitySupportFileManage), visible: true},
	{id: 810000073, parentID: 810000008, name: "Query files", typ: 3, sort: 413, webPerms: "support:file:query", visible: true},

	{id: 810000009, name: "Dictionary", typ: 2, sort: 402, path: "/support/dict", component: "support/dict/index", icon: "BookOutlined", visible: true},
	{id: 810000010, name: "Configuration", typ: 2, sort: 403, path: "/support/config", component: "support/config/config-list", icon: "SettingOutlined", visible: true},
	{id: 810000105, name: "Serial Numbers", typ: 2, sort: 404, path: "/support/serial-number", component: "support/serial-number/serial-number-list.vue", icon: "OrderedListOutlined", visible: true},
	{id: 810000106, name: "Messages", typ: 2, sort: 405, path: "/support/message", component: "support/message/message-list.vue", icon: "MessageOutlined", visible: true},
	{id: 810000111, name: "Feedback", typ: 2, sort: 406, path: "/support/feedback", component: "support/feedback/feedback-list.vue", icon: "CommentOutlined", visible: true},
	{id: 810000114, name: "Help Docs", typ: 2, sort: 407, path: "/support/help-doc", component: "support/help-doc/management/help-doc-manage-list.vue", icon: "QuestionCircleOutlined", visible: true},

	{id: 810000081, parentID: 810000002, name: "View dictionaries", typ: 3, sort: 421, capability: string(authz.CapabilitySupportDictRead), visible: true},
	{id: 810000082, parentID: 810000002, name: "Manage dictionaries", typ: 3, sort: 422, capability: string(authz.CapabilitySupportDictManage), visible: true},
	{id: 810000083, parentID: 810000009, name: "Add dictionary", typ: 3, sort: 423, webPerms: "support:dict:add", visible: true},
	{id: 810000084, parentID: 810000009, name: "Edit dictionary", typ: 3, sort: 424, webPerms: "support:dict:update", visible: true},
	{id: 810000085, parentID: 810000009, name: "Delete dictionary", typ: 3, sort: 425, webPerms: "support:dict:delete", visible: true},
	{id: 810000086, parentID: 810000009, name: "Add dictionary value", typ: 3, sort: 426, webPerms: "support:dictData:add", visible: true},
	{id: 810000087, parentID: 810000009, name: "Edit dictionary value", typ: 3, sort: 427, webPerms: "support:dictData:update", visible: true},
	{id: 810000088, parentID: 810000009, name: "Delete dictionary value", typ: 3, sort: 428, webPerms: "support:dictData:delete", visible: true},

	{id: 810000091, parentID: 810000002, name: "View configuration", typ: 3, sort: 223, capability: string(authz.CapabilitySupportConfigRead), visible: true},
	{id: 810000092, parentID: 810000002, name: "Manage configuration", typ: 3, sort: 224, capability: string(authz.CapabilitySupportConfigManage), visible: true},
	{id: 810000093, parentID: 810000010, name: "Query configuration", typ: 3, sort: 431, webPerms: "support:config:query", visible: true},
	{id: 810000094, parentID: 810000010, name: "Add configuration", typ: 3, sort: 432, webPerms: "support:config:add", visible: true},
	{id: 810000095, parentID: 810000010, name: "Edit configuration", typ: 3, sort: 433, webPerms: "support:config:update", visible: true},

	{id: 810000101, parentID: 810000002, name: "View serial numbers", typ: 3, sort: 225, capability: string(authz.CapabilitySupportSerialRead), visible: true},
	{id: 810000102, parentID: 810000002, name: "Generate serial numbers", typ: 3, sort: 226, capability: string(authz.CapabilitySupportSerialGenerate), visible: true},
	{id: 810000103, parentID: 810000105, name: "View serial records", typ: 3, sort: 441, webPerms: "support:serialNumber:record", visible: true},
	{id: 810000104, parentID: 810000105, name: "Generate serial number", typ: 3, sort: 442, webPerms: "support:serialNumber:generate", visible: true},

	{id: 810000107, parentID: 810000002, name: "View messages", typ: 3, sort: 227, capability: string(authz.CapabilitySupportMessageRead), visible: true},
	{id: 810000108, parentID: 810000002, name: "Send and delete messages", typ: 3, sort: 228, capability: string(authz.CapabilitySupportMessageSend), visible: true},
	{id: 810000109, parentID: 810000106, name: "Send message", typ: 3, sort: 451, webPerms: "support:message:send", visible: true},
	{id: 810000110, parentID: 810000106, name: "Delete message", typ: 3, sort: 452, webPerms: "support:message:delete", visible: true},

	{id: 810000112, parentID: 810000002, name: "View feedback", typ: 3, sort: 229, capability: string(authz.CapabilitySupportFeedbackRead), visible: true},
	{id: 810000113, parentID: 810000111, name: "Query feedback", typ: 3, sort: 461, webPerms: "feedback:query", visible: true},

	{id: 810000115, parentID: 810000002, name: "View help docs", typ: 3, sort: 230, capability: string(authz.CapabilitySupportHelpDocRead), visible: true},
	{id: 810000116, parentID: 810000002, name: "Manage help docs", typ: 3, sort: 231, capability: string(authz.CapabilitySupportHelpDocManage), visible: true},
	{id: 810000117, parentID: 810000114, name: "Query help docs", typ: 3, sort: 471, webPerms: "support:helpDoc:query", visible: true},
	{id: 810000118, parentID: 810000114, name: "Add help doc", typ: 3, sort: 472, webPerms: "support:helpDoc:add", visible: true},
	{id: 810000119, parentID: 810000114, name: "Edit help doc", typ: 3, sort: 473, webPerms: "support:helpDoc:update", visible: true},
	{id: 810000120, parentID: 810000114, name: "Delete help doc", typ: 3, sort: 474, webPerms: "support:helpDoc:delete", visible: true},
	{id: 810000121, parentID: 810000114, name: "Add help doc catalog", typ: 3, sort: 475, webPerms: "support:helpDocCatalog:addCategory", visible: true},
	{id: 810000122, parentID: 810000114, name: "Edit help doc catalog", typ: 3, sort: 476, webPerms: "support:helpDocCatalog:edit", visible: true},
	{id: 810000123, parentID: 810000114, name: "Delete help doc catalog", typ: 3, sort: 477, webPerms: "support:helpDocCatalog:delete", visible: true},

	{id: 810000127, parentID: 810000002, name: "View login logs", typ: 3, sort: 232, capability: string(authz.CapabilitySupportLoginLogRead), visible: true},
	{id: 810000128, parentID: 810000002, name: "Manage login protection", typ: 3, sort: 233, capability: string(authz.CapabilitySupportLoginLogManage), visible: true},
	{id: 810000129, parentID: 810000124, name: "Query login logs", typ: 3, sort: 481, webPerms: "support:loginLog:query", visible: true},

	{id: 810000131, parentID: 810000002, name: "View operate logs", typ: 3, sort: 234, capability: string(authz.CapabilitySupportOperateLogRead), visible: true},
	{id: 810000132, parentID: 810000130, name: "Query operate logs", typ: 3, sort: 491, webPerms: "support:operateLog:query", visible: true},
	{id: 810000133, parentID: 810000130, name: "View operate log detail", typ: 3, sort: 492, webPerms: "support:operateLog:detail", visible: true},

	{id: 810000135, parentID: 810000002, name: "View caches", typ: 3, sort: 235, capability: string(authz.CapabilitySupportCacheRead), visible: true},
	{id: 810000136, parentID: 810000002, name: "Manage caches", typ: 3, sort: 236, capability: string(authz.CapabilitySupportCacheManage), visible: true},
	{id: 810000139, parentID: 810000002, name: "View reloads", typ: 3, sort: 237, capability: string(authz.CapabilitySupportReloadRead), visible: true},
	{id: 810000140, parentID: 810000002, name: "Manage reloads", typ: 3, sort: 238, capability: string(authz.CapabilitySupportReloadManage), visible: true},
	{id: 810000141, parentID: 810000134, name: "Inspect cache keys", typ: 3, sort: 501, webPerms: "support:cache:keys", visible: true},
	{id: 810000142, parentID: 810000134, name: "Clear cache", typ: 3, sort: 502, webPerms: "support:cache:delete", visible: true},
	{id: 810000143, parentID: 810000137, name: "Execute reload", typ: 3, sort: 503, webPerms: "support:reload:execute", visible: true},
	{id: 810000144, parentID: 810000137, name: "View reload results", typ: 3, sort: 504, webPerms: "support:reload:result", visible: true},

	{id: 810000146, parentID: 810000002, name: "View jobs", typ: 3, sort: 239, capability: string(authz.CapabilitySupportJobRead), visible: true},
	{id: 810000147, parentID: 810000002, name: "Manage jobs", typ: 3, sort: 240, capability: string(authz.CapabilitySupportJobManage), visible: true},
	{id: 810000148, parentID: 810000002, name: "Execute jobs", typ: 3, sort: 241, capability: string(authz.CapabilitySupportJobExecute), visible: true},
	{id: 810000149, parentID: 810000145, name: "Query jobs", typ: 3, sort: 511, webPerms: "support:job:query", visible: true},
	{id: 810000150, parentID: 810000145, name: "Add job", typ: 3, sort: 512, webPerms: "support:job:add", visible: true},
	{id: 810000151, parentID: 810000145, name: "Update job", typ: 3, sort: 513, webPerms: "support:job:update", visible: true},
	{id: 810000152, parentID: 810000145, name: "Execute job", typ: 3, sort: 514, webPerms: "support:job:execute", visible: true},
	{id: 810000153, parentID: 810000145, name: "Query job logs", typ: 3, sort: 515, webPerms: "support:job:log:query", visible: true},
	{id: 810000154, parentID: 810000145, name: "Delete job", typ: 3, sort: 516, webPerms: "support:job:log:delete", visible: true},

	{id: 810000161, parentID: 810000002, name: "View dev tools", typ: 3, sort: 242, capability: string(authz.CapabilitySupportDevToolsRead), visible: true},
	{id: 810000162, parentID: 810000002, name: "View change logs", typ: 3, sort: 243, capability: string(authz.CapabilitySupportChangeLogRead), visible: true},
	{id: 810000163, parentID: 810000002, name: "Manage change logs", typ: 3, sort: 244, capability: string(authz.CapabilitySupportChangeLogManage), visible: true},
	{id: 810000164, parentID: 810000157, name: "Query change logs", typ: 3, sort: 521, webPerms: "support:changeLog:query", visible: true},
	{id: 810000165, parentID: 810000157, name: "Add change log", typ: 3, sort: 522, webPerms: "support:changeLog:add", visible: true},
	{id: 810000166, parentID: 810000157, name: "Update change log", typ: 3, sort: 523, webPerms: "support:changeLog:update", visible: true},
	{id: 810000167, parentID: 810000157, name: "Delete change log", typ: 3, sort: 524, webPerms: "support:changeLog:delete", visible: true},
	{id: 810000168, parentID: 810000157, name: "Batch delete change logs", typ: 3, sort: 525, webPerms: "support:changeLog:batchDelete", visible: true},

	{id: 810000171, parentID: 810000002, name: "View notices", typ: 3, sort: 245, capability: string(authz.CapabilityOANoticeRead), visible: true},
	{id: 810000172, parentID: 810000002, name: "Manage notices", typ: 3, sort: 246, capability: string(authz.CapabilityOANoticeManage), visible: true},
	{id: 810000173, parentID: 810000170, name: "Query notices", typ: 3, sort: 531, webPerms: "oa:notice:query", visible: true},
	{id: 810000174, parentID: 810000170, name: "Add notice", typ: 3, sort: 532, webPerms: "oa:notice:add", visible: true},
	{id: 810000175, parentID: 810000170, name: "Update notice", typ: 3, sort: 533, webPerms: "oa:notice:update", visible: true},
	{id: 810000176, parentID: 810000170, name: "Delete notice", typ: 3, sort: 534, webPerms: "oa:notice:delete", visible: true},
	{id: 810000177, parentID: 810000170, name: "Query notice types", typ: 3, sort: 535, webPerms: "oa:noticeType:query", visible: true},
	{id: 810000178, parentID: 810000170, name: "Manage notice types", typ: 3, sort: 536, webPerms: "oa:noticeType:manage", visible: true},
}

// operatorButtonIDs are current-operator-only points. They are seeded as
// system rows but cannot be assigned to a target role.
var operatorButtonIDs = map[uint64]bool{
	810000021: true,
	810000022: true,
	810000023: true,
	810000024: true,
	810000025: true,
	810000026: true,
	810000027: true,
	810000028: true,
	810000041: true,
	810000042: true,
	810000043: true,
	810000051: true,
	810000052: true,
	810000053: true,
	810000054: true,
	810000061: true,
	810000062: true,
	810000063: true,
	810000064: true,
	810000065: true,
	810000066: true,
	810000073: true,
	810000083: true,
	810000084: true,
	810000085: true,
	810000086: true,
	810000087: true,
	810000088: true,
	810000093: true,
	810000094: true,
	810000095: true,
	810000103: true,
	810000104: true,
	810000109: true,
	810000110: true,
	810000113: true,
	810000117: true,
	810000118: true,
	810000119: true,
	810000120: true,
	810000121: true,
	810000122: true,
	810000123: true,
	810000129: true,
	810000132: true,
	810000133: true,
	810000141: true,
	810000142: true,
	810000143: true,
	810000144: true,
	810000149: true,
	810000150: true,
	810000151: true,
	810000152: true,
	810000153: true,
	810000154: true,
	810000164: true,
	810000165: true,
	810000166: true,
	810000167: true,
	810000168: true,
	810000173: true,
	810000174: true,
	810000175: true,
	810000176: true,
	810000177: true,
	810000178: true,
}
