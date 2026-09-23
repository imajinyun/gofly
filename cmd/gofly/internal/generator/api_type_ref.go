package generator

import (
	"errors"
	"fmt"
	"strings"
)

type apiTypeKind uint8

const (
	apiTypeKindNamed apiTypeKind = iota
	apiTypeKindPointer
	apiTypeKindSlice
	apiTypeKindMap
)

type apiTypeRef struct {
	Kind apiTypeKind
	Name string
	Key  *apiTypeRef
	Elem *apiTypeRef
}

func parseAPITypeRef(raw string) (apiTypeRef, error) {
	parser := apiTypeRefParser{input: strings.TrimSpace(raw)}
	ref, err := parser.parse()
	if err != nil {
		return apiTypeRef{}, fmt.Errorf("parse api type %q: %w", raw, err)
	}
	parser.skipSpaces()
	if parser.pos != len(parser.input) {
		return apiTypeRef{}, fmt.Errorf("parse api type %q: unexpected suffix %q", raw, parser.input[parser.pos:])
	}
	return ref, nil
}

func (ref apiTypeRef) String() string {
	switch ref.Kind {
	case apiTypeKindNamed:
		return ref.Name
	case apiTypeKindPointer:
		return "*" + apiTypeRefString(ref.Elem)
	case apiTypeKindSlice:
		return "[]" + apiTypeRefString(ref.Elem)
	case apiTypeKindMap:
		return "map[" + apiTypeRefString(ref.Key) + "]" + apiTypeRefString(ref.Elem)
	default:
		return ""
	}
}

func (ref apiTypeRef) BaseNames() []string {
	var names []string
	var visit func(apiTypeRef)
	visit = func(current apiTypeRef) {
		switch current.Kind {
		case apiTypeKindNamed:
			names = append(names, current.Name)
		case apiTypeKindPointer, apiTypeKindSlice:
			if current.Elem != nil {
				visit(*current.Elem)
			}
		case apiTypeKindMap:
			if current.Key != nil {
				visit(*current.Key)
			}
			if current.Elem != nil {
				visit(*current.Elem)
			}
		}
	}
	visit(ref)
	return names
}

func apiTypeRefString(ref *apiTypeRef) string {
	if ref == nil {
		return ""
	}
	return ref.String()
}

type apiTypeRefParser struct {
	input string
	pos   int
}

func (p *apiTypeRefParser) parse() (apiTypeRef, error) {
	p.skipSpaces()
	if p.consume("*") {
		elem, err := p.parse()
		if err != nil {
			return apiTypeRef{}, err
		}
		return apiTypeRef{Kind: apiTypeKindPointer, Elem: &elem}, nil
	}
	if p.consume("[]") {
		elem, err := p.parse()
		if err != nil {
			return apiTypeRef{}, err
		}
		return apiTypeRef{Kind: apiTypeKindSlice, Elem: &elem}, nil
	}
	if p.consume("map") {
		return p.parseMap()
	}
	name := p.parseName()
	if name == "" {
		return apiTypeRef{}, errors.New("type name is required")
	}
	return apiTypeRef{Kind: apiTypeKindNamed, Name: name}, nil
}

func (p *apiTypeRefParser) parseMap() (apiTypeRef, error) {
	p.skipSpaces()
	if !p.consume("[") {
		return apiTypeRef{}, errors.New("map key opening bracket is required")
	}
	key, err := p.parse()
	if err != nil {
		return apiTypeRef{}, err
	}
	if key.Kind != apiTypeKindNamed || !isAPIMapKeyType(key.Name) {
		return apiTypeRef{}, fmt.Errorf("unsupported map key type %s", key.String())
	}
	p.skipSpaces()
	if !p.consume("]") {
		return apiTypeRef{}, errors.New("map key closing bracket is required")
	}
	elem, err := p.parse()
	if err != nil {
		return apiTypeRef{}, err
	}
	return apiTypeRef{Kind: apiTypeKindMap, Key: &key, Elem: &elem}, nil
}

func (p *apiTypeRefParser) parseName() string {
	p.skipSpaces()
	start := p.pos
	for p.pos < len(p.input) {
		char := p.input[p.pos]
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '.' || char == '{' || char == '}' {
			p.pos++
			continue
		}
		break
	}
	return p.input[start:p.pos]
}

func (p *apiTypeRefParser) skipSpaces() {
	for p.pos < len(p.input) && (p.input[p.pos] == ' ' || p.input[p.pos] == '\t') {
		p.pos++
	}
}

func (p *apiTypeRefParser) consume(value string) bool {
	if !strings.HasPrefix(p.input[p.pos:], value) {
		return false
	}
	p.pos += len(value)
	return true
}

func isAPIMapKeyType(name string) bool {
	switch name {
	case "string", "bool", "byte", "rune", "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		return true
	default:
		return false
	}
}

func apiTypeRefOrNamed(raw string) apiTypeRef {
	ref, err := parseAPITypeRef(raw)
	if err == nil {
		return ref
	}
	return apiTypeRef{Kind: apiTypeKindNamed, Name: strings.TrimSpace(raw)}
}

func apiTypeRefElement(ref apiTypeRef) apiTypeRef {
	if ref.Elem == nil {
		return apiTypeRef{}
	}
	return *ref.Elem
}

func apiGoTypeRef(ref apiTypeRef) string {
	switch ref.Kind {
	case apiTypeKindPointer:
		return "*" + apiGoTypeRef(apiTypeRefElement(ref))
	case apiTypeKindSlice:
		return "[]" + apiGoTypeRef(apiTypeRefElement(ref))
	case apiTypeKindMap:
		return "map[" + apiGoTypeRef(*ref.Key) + "]" + apiGoTypeRef(apiTypeRefElement(ref))
	default:
		return apiGoNamedType(ref.Name)
	}
}

func apiGoNamedType(name string) string {
	switch name {
	case "string", "bool", "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "float32", "float64", "byte", "rune", "any", "interface{}", "time.Time":
		return name
	default:
		return exportName(name)
	}
}

func apiTypeScriptTypeRef(ref apiTypeRef) string {
	switch ref.Kind {
	case apiTypeKindPointer:
		return apiTypeScriptTypeRef(apiTypeRefElement(ref))
	case apiTypeKindSlice:
		return apiTypeScriptTypeRef(apiTypeRefElement(ref)) + "[]"
	case apiTypeKindMap:
		return "Record<" + apiTypeScriptTypeRef(*ref.Key) + ", " + apiTypeScriptTypeRef(apiTypeRefElement(ref)) + ">"
	default:
		switch ref.Name {
		case "string":
			return "string"
		case "bool":
			return "boolean"
		case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "float32", "float64", "byte", "rune":
			return "number"
		case "any", "interface{}":
			return "unknown"
		default:
			return exportName(ref.Name)
		}
	}
}

func apiDartTypeRef(ref apiTypeRef) string {
	switch ref.Kind {
	case apiTypeKindPointer:
		return apiDartTypeRef(apiTypeRefElement(ref))
	case apiTypeKindSlice:
		return "List<" + apiDartTypeRef(apiTypeRefElement(ref)) + ">"
	case apiTypeKindMap:
		return "Map<" + apiDartTypeRef(*ref.Key) + ", " + apiDartTypeRef(apiTypeRefElement(ref)) + ">"
	default:
		switch ref.Name {
		case "string":
			return "String"
		case "bool":
			return "bool"
		case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "byte", "rune":
			return "int"
		case "float32", "float64":
			return "double"
		case "any", "interface{}":
			return "Object"
		default:
			return exportName(ref.Name)
		}
	}
}

func apiJavaTypeRef(ref apiTypeRef) string {
	switch ref.Kind {
	case apiTypeKindPointer:
		return apiJavaTypeRef(apiTypeRefElement(ref))
	case apiTypeKindSlice:
		return "java.util.List<" + apiJavaTypeRef(apiTypeRefElement(ref)) + ">"
	case apiTypeKindMap:
		return "java.util.Map<" + apiJavaTypeRef(*ref.Key) + ", " + apiJavaTypeRef(apiTypeRefElement(ref)) + ">"
	default:
		return javaBoxedType(ref.Name)
	}
}

func apiKotlinTypeRef(ref apiTypeRef) string {
	switch ref.Kind {
	case apiTypeKindPointer:
		return apiKotlinTypeRef(apiTypeRefElement(ref))
	case apiTypeKindSlice:
		return "List<" + apiKotlinTypeRef(apiTypeRefElement(ref)) + ">"
	case apiTypeKindMap:
		return "Map<" + apiKotlinTypeRef(*ref.Key) + ", " + apiKotlinTypeRef(apiTypeRefElement(ref)) + ">"
	default:
		switch ref.Name {
		case "string":
			return "String"
		case "bool":
			return "Boolean"
		case "int", "int8", "int16", "int32", "uint8", "uint16", "uint32", "byte", "rune":
			return "Int"
		case "int64", "uint", "uint64":
			return "Long"
		case "float32":
			return "Float"
		case "float64":
			return "Double"
		case "any", "interface{}":
			return "Any"
		default:
			return exportName(ref.Name)
		}
	}
}
