package generator

import (
	"fmt"
	"sort"
	"strings"
)

type protoTypeRef struct {
	GoType string
	Import string
}

func newProtoTypeResolver(doc IDLDocument) protoTypeResolver {
	imports := make(map[string]IDLImportedProto, len(doc.ImportedProtos))
	for _, item := range doc.ImportedProtos {
		imports[item.ProtoPackage] = item
	}
	return protoTypeResolver{mainPackage: doc.Package, imports: imports}
}

type protoTypeResolver struct {
	mainPackage string
	imports     map[string]IDLImportedProto
}

func (r protoTypeResolver) methodRequest(method IDLMethod) protoTypeRef {
	return r.resolve(method.ProtoRequest, method.Request)
}

func (r protoTypeResolver) methodResponse(method IDLMethod) protoTypeRef {
	return r.resolve(method.ProtoResponse, method.Response)
}

func (r protoTypeResolver) resolve(raw, fallback string) protoTypeRef {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), ".")
	if raw == "" {
		raw = strings.TrimSpace(fallback)
	}
	if !strings.Contains(raw, ".") || strings.HasPrefix(raw, "google.protobuf.") {
		return protoTypeRef{GoType: exportName(lastIdent(raw))}
	}
	var matchedPackage string
	var matched IDLImportedProto
	for protoPackage, item := range r.imports {
		if raw == protoPackage || !strings.HasPrefix(raw, protoPackage+".") {
			continue
		}
		if len(protoPackage) > len(matchedPackage) {
			matchedPackage = protoPackage
			matched = item
		}
	}
	if matchedPackage != "" {
		typeName := exportName(strings.TrimPrefix(raw, matchedPackage+"."))
		return protoTypeRef{GoType: lowerCamel(matched.Alias) + "." + typeName, Import: matched.GoPackage}
	}
	if r.mainPackage != "" && strings.HasPrefix(raw, r.mainPackage+".") {
		return protoTypeRef{GoType: exportName(strings.TrimPrefix(raw, r.mainPackage+"."))}
	}
	return protoTypeRef{GoType: exportName(lastIdent(raw))}
}

func protoTypeImports(doc IDLDocument) ([]IDLImportedProto, error) {
	resolver := newProtoTypeResolver(doc)
	imports := make(map[string]IDLImportedProto)
	for _, service := range doc.Services {
		for _, method := range service.Methods {
			for _, ref := range []protoTypeRef{resolver.methodRequest(method), resolver.methodResponse(method)} {
				if ref.Import == "" {
					continue
				}
				item, ok := resolver.importForPath(ref.Import)
				if !ok {
					return nil, fmt.Errorf("resolve imported proto type %q", ref.GoType)
				}
				imports[item.GoPackage] = item
			}
		}
	}
	aliases := make(map[string]string, len(imports))
	for path, item := range imports {
		alias := lowerCamel(item.Alias)
		if existing, ok := aliases[alias]; ok && existing != path {
			return nil, fmt.Errorf("imported proto packages %q and %q use Go alias %q", existing, path, alias)
		}
		aliases[alias] = path
	}
	out := make([]IDLImportedProto, 0, len(imports))
	for _, item := range imports {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GoPackage < out[j].GoPackage })
	return out, nil
}

func (r protoTypeResolver) importForPath(path string) (IDLImportedProto, bool) {
	for _, item := range r.imports {
		if item.GoPackage == path {
			return item, true
		}
	}
	return IDLImportedProto{}, false
}
