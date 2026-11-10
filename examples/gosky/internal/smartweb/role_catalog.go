package smartweb

import (
	"slices"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

type capabilityMenuDefinition struct {
	menuID     uint64
	capability authz.Capability
	name       string
}

var smartWebCapabilityMenus = []capabilityMenuDefinition{
	{menuID: 810000017, capability: authz.CapabilityProjectUpdate, name: "Update related Projects"},
	{menuID: 810000018, capability: authz.CapabilityProjectDelete, name: "Delete owned Projects"},
	{menuID: 810000019, capability: authz.CapabilityProjectManageAny, name: "Manage all tenant Projects (scope only)"},
	{menuID: 810000031, capability: authz.CapabilityAuthorizationRoleRead, name: "View tenant roles and employees"},
	{menuID: 810000032, capability: authz.CapabilityAuthorizationRoleScopeManage, name: "Edit Project read scope"},
	{menuID: 810000033, capability: authz.CapabilityAuthorizationRoleMemberAdd, name: "Assign tenant roles"},
	{menuID: 810000034, capability: authz.CapabilityAuthorizationRoleMemberRemove, name: "Remove tenant roles"},
	{menuID: 810000035, capability: authz.CapabilityAuthorizationPolicyEventRead, name: "Read policy audit events"},
	{menuID: 810000036, capability: authz.CapabilitySystemUserManage, name: "Manage tenant users"},
	{menuID: 810000037, capability: authz.CapabilitySystemMenuManage, name: "Manage tenant menus"},

	{menuID: SmartWebCapabilityProjectReadMenuID, capability: authz.CapabilityProjectRead, name: "Read related Projects"},
	{menuID: SmartWebCapabilityProjectReadAnyMenuID, capability: authz.CapabilityProjectReadAny, name: "Read all tenant Projects"},
	{menuID: SmartWebCapabilityProjectMemberMenuID, capability: authz.CapabilityProjectMemberManage, name: "Manage Project collaborators"},
	{menuID: SmartWebCapabilityRoleManageMenuID, capability: authz.CapabilityAuthorizationRoleManage, name: "Edit role permissions"},
	{menuID: SmartWebCapabilityGreeterHelloMenuID, capability: authz.CapabilityGreeterHello, name: "Call Greeter"},
	{menuID: SmartWebCapabilityGreeterWatchMenuID, capability: authz.CapabilityGreeterWatch, name: "Watch Greeter stream"},
	{menuID: SmartWebCapabilityFileReadMenuID, capability: authz.CapabilitySupportFileRead, name: "View files"},
	{menuID: SmartWebCapabilityFileManageMenuID, capability: authz.CapabilitySupportFileManage, name: "Upload and manage files"},
	{menuID: SmartWebCapabilityDictReadMenuID, capability: authz.CapabilitySupportDictRead, name: "View dictionaries"},
	{menuID: SmartWebCapabilityDictManageMenuID, capability: authz.CapabilitySupportDictManage, name: "Manage dictionaries"},
	{menuID: SmartWebCapabilityConfigReadMenuID, capability: authz.CapabilitySupportConfigRead, name: "View configuration"},
	{menuID: SmartWebCapabilityConfigManageMenuID, capability: authz.CapabilitySupportConfigManage, name: "Manage configuration"},
	{menuID: SmartWebCapabilitySerialReadMenuID, capability: authz.CapabilitySupportSerialRead, name: "View serial numbers"},
	{menuID: SmartWebCapabilitySerialGenMenuID, capability: authz.CapabilitySupportSerialGenerate, name: "Generate serial numbers"},
	{menuID: SmartWebCapabilityMessageReadMenuID, capability: authz.CapabilitySupportMessageRead, name: "View messages"},
	{menuID: SmartWebCapabilityMessageSendMenuID, capability: authz.CapabilitySupportMessageSend, name: "Send and delete messages"},
	{menuID: SmartWebCapabilityFeedbackReadMenuID, capability: authz.CapabilitySupportFeedbackRead, name: "View feedback"},
	{menuID: SmartWebCapabilityHelpDocReadMenuID, capability: authz.CapabilitySupportHelpDocRead, name: "View help docs"},
	{menuID: SmartWebCapabilityHelpDocManageMenuID, capability: authz.CapabilitySupportHelpDocManage, name: "Manage help docs"},
	{menuID: SmartWebCapabilityLoginLogReadMenuID, capability: authz.CapabilitySupportLoginLogRead, name: "View login logs"},
	{menuID: SmartWebCapabilityLoginLogManageMenuID, capability: authz.CapabilitySupportLoginLogManage, name: "Manage login protection"},
	{menuID: SmartWebCapabilityOperateLogReadMenuID, capability: authz.CapabilitySupportOperateLogRead, name: "View operate logs"},
	{menuID: SmartWebCapabilityCacheReadMenuID, capability: authz.CapabilitySupportCacheRead, name: "View caches"},
	{menuID: SmartWebCapabilityCacheManageMenuID, capability: authz.CapabilitySupportCacheManage, name: "Manage caches"},
	{menuID: SmartWebCapabilityReloadReadMenuID, capability: authz.CapabilitySupportReloadRead, name: "View reloads"},
	{menuID: SmartWebCapabilityReloadManageMenuID, capability: authz.CapabilitySupportReloadManage, name: "Manage reloads"},
	{menuID: SmartWebCapabilityJobReadMenuID, capability: authz.CapabilitySupportJobRead, name: "View jobs"},
	{menuID: SmartWebCapabilityJobManageMenuID, capability: authz.CapabilitySupportJobManage, name: "Manage jobs"},
	{menuID: SmartWebCapabilityJobExecuteMenuID, capability: authz.CapabilitySupportJobExecute, name: "Execute jobs"},
	{menuID: SmartWebCapabilityDevToolsReadMenuID, capability: authz.CapabilitySupportDevToolsRead, name: "View dev tools"},
	{menuID: SmartWebCapabilityChangeLogReadMenuID, capability: authz.CapabilitySupportChangeLogRead, name: "View change logs"},
	{menuID: SmartWebCapabilityChangeLogManageMenuID, capability: authz.CapabilitySupportChangeLogManage, name: "Manage change logs"},
	{menuID: SmartWebCapabilityNoticeReadMenuID, capability: authz.CapabilityOANoticeRead, name: "View notices"},
	{menuID: SmartWebCapabilityNoticeManageMenuID, capability: authz.CapabilityOANoticeManage, name: "Manage notices"},
}

func capabilityMenuIDs(capabilities []authz.Capability) []uint64 {
	selected := make([]uint64, 0, len(capabilities))
	for _, definition := range smartWebCapabilityMenus {
		if containsCapability(capabilities, definition.capability) {
			selected = append(selected, definition.menuID)
		}
	}
	return selected
}

func capabilitiesFromMenuIDs(menuIDs []uint64) ([]authz.Capability, *coreerrors.Error) {
	selected := make(map[uint64]struct{}, len(menuIDs))
	for _, menuID := range menuIDs {
		selected[menuID] = struct{}{}
	}
	structuralIDs := smartWebStructuralMenuIDs()
	for menuID := range selected {
		if slices.Contains(structuralIDs, menuID) {
			continue
		}
		if _, ok := capabilityForMenuID(menuID); !ok {
			return nil, coreerrors.New(coreerrors.CodeInvalidArgument, "role menu contains an unsupported capability")
		}
	}
	capabilities := make([]authz.Capability, 0, len(selected))
	for _, definition := range smartWebCapabilityMenus {
		if _, ok := selected[definition.menuID]; ok {
			capabilities = append(capabilities, definition.capability)
		}
	}
	return capabilities, nil
}

func capabilityForMenuID(menuID uint64) (authz.Capability, bool) {
	for _, definition := range smartWebCapabilityMenus {
		if definition.menuID == menuID {
			return definition.capability, true
		}
	}
	return "", false
}

func defaultRoleCapabilities(role string) []authz.Capability {
	return authz.DefaultRoleCapabilities(role)
}

func containsCapability(capabilities []authz.Capability, want authz.Capability) bool {
	for _, capability := range capabilities {
		if capability == want {
			return true
		}
	}
	return false
}

func normalizeCapabilities(capabilities []authz.Capability) []authz.Capability {
	unique := make(map[authz.Capability]struct{}, len(capabilities))
	for _, capability := range capabilities {
		unique[capability] = struct{}{}
	}
	out := make([]authz.Capability, 0, len(unique))
	for _, definition := range smartWebCapabilityMenus {
		if _, ok := unique[definition.capability]; ok {
			out = append(out, definition.capability)
		}
	}
	return out
}
