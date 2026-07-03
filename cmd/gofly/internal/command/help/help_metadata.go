package help

var topLevelHelpAliases = map[string]string{
	"generate":  "gen",
	"migration": "migrate",
	"tools":     "ai",
}

var nestedHelpAliases = map[string]map[string]string{
	"api": {
		"fmt":        "format",
		"gen":        "go",
		"docs":       "doc",
		"validate":   "check",
		"break":      "breaking",
		"typescript": "ts",
		"javascript": "js",
		"kt":         "kotlin",
		"routes":     "route",
	},
	"rpc": {
		"break":        "breaking",
		"tpl":          "template",
		"inspect":      "idl",
		"thrift2proto": "thrift",
	},
	"model": {
		"postgres":   "pg",
		"postgresql": "pg",
	},
	"feature": {
		"ls": "list",
	},
	"plugin": {
		"ls": "list",
	},
	"kube": {
		"deployment": "deploy",
		"svc":        "service",
		"ing":        "ingress",
		"cm":         "configmap",
	},
	"template": {
		"ls": "list",
	},
	"env": {
		"install": "check",
	},
	"migrate": {
		"new": "create",
	},
	"completion": {
		"pwsh": "powershell",
	},
}

func TopLevelAliases() map[string]string {
	aliases := make(map[string]string, len(topLevelHelpAliases))
	for alias, canonical := range topLevelHelpAliases {
		aliases[alias] = canonical
	}
	return aliases
}

func NestedAliases() map[string]map[string]string {
	aliases := make(map[string]map[string]string, len(nestedHelpAliases))
	for parent, nested := range nestedHelpAliases {
		nestedCopy := make(map[string]string, len(nested))
		for alias, canonical := range nested {
			nestedCopy[alias] = canonical
		}
		aliases[parent] = nestedCopy
	}
	return aliases
}
