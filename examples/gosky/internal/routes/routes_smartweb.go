package routes

import (
	"net/http"

	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smartcache"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smartconfig"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smartdepartment"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smartdevtools"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smartdict"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smartemployee"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smartencrypt"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smartfeedback"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smartfile"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smarthelpdoc"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smartjob"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smartlogin"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smartmenu"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smartmessage"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smartnotice"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smartoperatelog"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smartposition"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smartprotect"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smartreload"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smartrole"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smartserialnumber"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smarttablecolumn"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

// registerSmartWebCompatibilityRoutes installs the unmodified SmartAdmin
// client's root-level contracts (/login/**, /menu/**, /role/**, ...). These
// all belong to the admin identity realm and require an admin token; the
// anonymous login/captcha endpoints live in registerPublicRoutes.
func registerSmartWebCompatibilityRoutes(server *rest.Server, svcCtx *svc.ServiceContext, interceptor authn.Interceptor) {
	if svcCtx.CurrentSmartWebService() == nil {
		return
	}
	server.AddRoutes([]rest.Route{
		{Method: http.MethodGet, Path: "/login/getLoginInfo", Handler: smartlogin.LoginInfoHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/login/logout", Handler: smartlogin.LogoutHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/dict/getAllDict", Handler: smartdict.GetAllHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/dict/getAllDictData", Handler: smartdict.GetAllDataHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/dict/queryPage", Handler: smartdict.QueryPageHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/dict/add", Handler: smartdict.AddHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/dict/update", Handler: smartdict.UpdateHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/dict/batchDelete", Handler: smartdict.BatchDeleteHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/dict/updateDisabled/{dictId}", Handler: smartdict.UpdateDisabledHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/dict/dictData/queryDictData/{dictId}", Handler: smartdict.QueryDataHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/dict/dictData/add", Handler: smartdict.AddDataHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/dict/dictData/update", Handler: smartdict.UpdateDataHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/dict/dictData/batchDelete", Handler: smartdict.BatchDeleteDataHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/dict/dictData/updateDisabled/{dictDataId}", Handler: smartdict.UpdateDataDisabledHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/config/query", Handler: smartconfig.QueryHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/config/add", Handler: smartconfig.AddHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/config/update", Handler: smartconfig.UpdateHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/config/queryByKey", Handler: smartconfig.QueryByKeyHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/tableColumn/update", Handler: smarttablecolumn.UpdateHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/tableColumn/getColumns/{tableId}", Handler: smarttablecolumn.GetColumnsHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/tableColumn/delete/{tableId}", Handler: smarttablecolumn.DeleteHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/serialNumber/all", Handler: smartserialnumber.AllHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/serialNumber/generate", Handler: smartserialnumber.GenerateHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/serialNumber/queryRecord", Handler: smartserialnumber.QueryRecordHandler(svcCtx)},

		{Method: http.MethodPost, Path: "/support/message/queryMyMessage", Handler: smartmessage.QueryMyMessageHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/message/getUnreadCount", Handler: smartmessage.UnreadCountHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/message/read/{messageId}", Handler: smartmessage.ReadHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/message/query", Handler: smartmessage.QueryHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/message/sendMessages", Handler: smartmessage.SendHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/message/delete/{messageId}", Handler: smartmessage.DeleteHandler(svcCtx)},

		{Method: http.MethodPost, Path: "/support/feedback/add", Handler: smartfeedback.AddHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/feedback/query", Handler: smartfeedback.QueryHandler(svcCtx)},

		{Method: http.MethodGet, Path: "/support/helpDoc/helpDocCatalog/getAll", Handler: smarthelpdoc.GetAllCatalogsHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/helpDoc/helpDocCatalog/add", Handler: smarthelpdoc.AddCatalogHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/helpDoc/helpDocCatalog/update", Handler: smarthelpdoc.UpdateCatalogHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/helpDoc/helpDocCatalog/delete/{helpDocCatalogId}", Handler: smarthelpdoc.DeleteCatalogHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/helpDoc/query", Handler: smarthelpdoc.QueryHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/helpDoc/getDetail/{helpDocId}", Handler: smarthelpdoc.GetDetailHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/helpDoc/add", Handler: smarthelpdoc.AddHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/helpDoc/update", Handler: smarthelpdoc.UpdateHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/helpDoc/delete/{helpDocId}", Handler: smarthelpdoc.DeleteHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/helpDoc/queryHelpDocByRelationId/{relationId}", Handler: smarthelpdoc.RelationHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/helpDoc/user/queryAllHelpDocList", Handler: smarthelpdoc.AllHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/helpDoc/user/view/{helpDocId}", Handler: smarthelpdoc.ViewHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/helpDoc/user/queryViewRecord", Handler: smarthelpdoc.ViewRecordHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/loginLog/page/query", Handler: smartprotect.QueryLogsHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/loginLog/page/query/login", Handler: smartprotect.QueryOwnLogsHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/protect/loginFail/queryPage", Handler: smartprotect.QueryFailsHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/protect/loginFail/batchDelete", Handler: smartprotect.DeleteFailsHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/protect/level3protect/getConfig", Handler: smartprotect.GetConfigHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/protect/level3protect/updateConfig", Handler: smartprotect.UpdateConfigHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/operateLog/page/query", Handler: smartoperatelog.QueryHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/operateLog/detail/{operateLogId}", Handler: smartoperatelog.DetailHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/operateLog/page/query/login", Handler: smartoperatelog.QueryOwnHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/cache/names", Handler: smartcache.NamesHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/cache/keys/{cacheName}", Handler: smartcache.KeysHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/cache/remove/{cacheName}", Handler: smartcache.RemoveHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/reload/query", Handler: smartreload.QueryHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/reload/result/{tag}", Handler: smartreload.ResultHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/reload/update", Handler: smartreload.UpdateHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/job/query", Handler: smartjob.QueryHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/job/{jobId}", Handler: smartjob.DetailHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/job/add", Handler: smartjob.AddHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/job/update", Handler: smartjob.UpdateHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/job/update/enabled", Handler: smartjob.UpdateEnabledHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/job/execute", Handler: smartjob.ExecuteHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/job/delete", Handler: smartjob.DeleteHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/job/log/query", Handler: smartjob.LogQueryHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/apiEncrypt/testRequestEncrypt", Handler: smartencrypt.RequestEncryptHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/apiEncrypt/testResponseEncrypt", Handler: smartencrypt.ResponseEncryptHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/apiEncrypt/testDecryptAndEncrypt", Handler: smartencrypt.BothHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/apiEncrypt/testArray", Handler: smartencrypt.ArrayHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/dataMasking/demo/query", Handler: smartdevtools.MaskingDemoHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/heartBeat/query", Handler: smartdevtools.HeartBeatHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/changeLog/queryPage", Handler: smartdevtools.ChangeLogQueryHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/changeLog/getDetail/{changeLogId}", Handler: smartdevtools.ChangeLogDetailHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/changeLog/add", Handler: smartdevtools.ChangeLogAddHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/changeLog/update", Handler: smartdevtools.ChangeLogUpdateHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/changeLog/delete/{changeLogId}", Handler: smartdevtools.ChangeLogDeleteHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/changeLog/batchDelete", Handler: smartdevtools.ChangeLogBatchDeleteHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/dataTracer/query", Handler: smartdevtools.DataTracerHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/codeGenerator/table/queryTableList", Handler: smartdevtools.CodeGeneratorTableListHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/codeGenerator/table/getTableColumns/{table}", Handler: smartdevtools.CodeGeneratorColumnsHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/codeGenerator/table/getConfig/{table}", Handler: smartdevtools.CodeGeneratorConfigHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/codeGenerator/table/updateConfig", Handler: smartdevtools.CodeGeneratorUpdateConfigHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/codeGenerator/code/preview", Handler: smartdevtools.CodeGeneratorPreviewHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/codeGenerator/code/download/{tableName}", Handler: smartdevtools.CodeGeneratorDownloadHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/oa/noticeType/getAll", Handler: smartnotice.GetAllTypesHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/oa/noticeType/add/{name}", Handler: smartnotice.AddTypeHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/oa/noticeType/update/{noticeTypeId}/{name}", Handler: smartnotice.UpdateTypeHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/oa/noticeType/delete/{noticeTypeId}", Handler: smartnotice.DeleteTypeHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/oa/notice/query", Handler: smartnotice.QueryHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/oa/notice/add", Handler: smartnotice.AddHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/oa/notice/update", Handler: smartnotice.UpdateHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/oa/notice/getUpdateVO/{noticeId}", Handler: smartnotice.UpdateFormHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/oa/notice/delete/{noticeId}", Handler: smartnotice.DeleteHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/oa/notice/employee/view/{noticeId}", Handler: smartnotice.ViewHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/oa/notice/employee/query", Handler: smartnotice.EmployeeQueryHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/oa/notice/employee/queryViewRecord", Handler: smartnotice.ViewRecordHandler(svcCtx)},

		{Method: http.MethodGet, Path: "/menu/query", Handler: smartmenu.QueryHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/menu/tree", Handler: smartmenu.TreeHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/menu/auth/url", Handler: smartmenu.AuthorizationURLsHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/menu/add", Handler: smartmenu.AddMenuHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/menu/update", Handler: smartmenu.UpdateMenuHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/menu/batchDelete", Handler: smartmenu.DeleteMenuHandler(svcCtx)},

		{Method: http.MethodGet, Path: "/role/getAll", Handler: smartrole.ListRolesHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/role/add", Handler: smartrole.RoleAddHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/role/update", Handler: smartrole.RoleUpdateHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/role/delete/{id}", Handler: smartrole.RoleDeleteHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/role/menu/getRoleSelectedMenu/{id}", Handler: smartrole.RoleMenuSelectionHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/role/menu/updateRoleMenu", Handler: smartrole.UpdateRoleMenuHandler(svcCtx)},

		{Method: http.MethodGet, Path: "/dataScope/list", Handler: smartrole.DataScopesHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/role/dataScope/getRoleDataScopeList/{id}", Handler: smartrole.RoleDataScopeSelectionHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/role/dataScope/updateRoleDataScopeList", Handler: smartrole.UpdateRoleDataScopesHandler(svcCtx)},

		{Method: http.MethodPost, Path: "/role/employee/queryEmployee", Handler: smartrole.RoleMembersHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/role/employee/getAllEmployeeByRoleId/{id}", Handler: smartrole.AllRoleMembersHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/role/employee/batchAddRoleEmployee", Handler: smartrole.AddRoleEmployeesHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/role/employee/batchRemoveRoleEmployee", Handler: smartrole.RemoveRoleEmployeesHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/role/employee/removeEmployee", Handler: smartrole.RemoveRoleEmployeeHandler(svcCtx)},

		// Departments
		{Method: http.MethodGet, Path: "/department/listAll", Handler: smartdepartment.ListAllHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/department/treeList", Handler: smartdepartment.TreeHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/department/add", Handler: smartdepartment.AddHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/department/update", Handler: smartdepartment.UpdateHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/department/delete/{id}", Handler: smartdepartment.DeleteHandler(svcCtx)},

		// Positions
		{Method: http.MethodPost, Path: "/position/queryPage", Handler: smartposition.QueryPageHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/position/queryList", Handler: smartposition.QueryListHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/position/add", Handler: smartposition.AddHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/position/update", Handler: smartposition.UpdateHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/position/delete/{id}", Handler: smartposition.DeleteHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/position/batchDelete", Handler: smartposition.BatchDeleteHandler(svcCtx)},

		// Employees
		{Method: http.MethodPost, Path: "/employee/query", Handler: smartemployee.QueryHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/employee/queryAll", Handler: smartemployee.QueryAllHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/employee/add", Handler: smartemployee.AddHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/employee/update", Handler: smartemployee.UpdateHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/employee/delete/{id}", Handler: smartemployee.DeleteHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/employee/update/batch/delete", Handler: smartemployee.BatchDeleteHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/employee/update/batch/department", Handler: smartemployee.BatchDepartmentHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/employee/update/disabled/{id}", Handler: smartemployee.DisabledHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/employee/update/password/reset/{id}", Handler: smartemployee.ResetPasswordHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/employee/getPasswordComplexityEnabled", Handler: smartemployee.PasswordComplexityHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/employee/update/password", Handler: smartemployee.ChangeOwnPasswordHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/employee/getAllEmployeeByDepartmentId/{id}", Handler: smartemployee.ByDepartmentHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/employee/update/center", Handler: smartemployee.UpdateCenterHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/employee/update/avatar", Handler: smartemployee.UpdateAvatarHandler(svcCtx)},

		{Method: http.MethodPost, Path: "/support/file/upload", Handler: smartfile.UploadHandler(svcCtx)},
		{Method: http.MethodPost, Path: "/support/file/queryPage", Handler: smartfile.QueryPageHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/file/getFileUrl", Handler: smartfile.GetURLHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/support/file/downLoad", Handler: smartfile.DownloadHandler(svcCtx)},
	}, rest.WithMiddlewares(smartweb.AuthMiddleware(interceptor), smartoperatelog.Middleware(svcCtx)))
}
