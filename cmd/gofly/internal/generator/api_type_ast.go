package generator

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strconv"
	"strings"
)

// apiLineLexer tracks comment and raw-string state across .api source lines.
// It lets the parser inspect code without being confused by braces, keywords,
// or comment markers inside strings and block comments.
type apiLineLexer struct {
	inBlockComment bool
	inRawString    bool
}

// line returns the line with comments blanked out (code) and the same line
// with both comments and string contents blanked out (mask). Both results keep
// the original byte length so structural offsets remain stable.
func (l *apiLineLexer) line(raw string) (code, mask string) {
	codeBytes := []byte(raw)
	maskBytes := make([]byte, len(raw))
	for index := range maskBytes {
		maskBytes[index] = ' '
	}
	for index := 0; index < len(raw); {
		if l.inBlockComment {
			end := strings.Index(raw[index:], "*/")
			if end < 0 {
				for pos := index; pos < len(raw); pos++ {
					codeBytes[pos] = ' '
				}
				return string(codeBytes), string(maskBytes)
			}
			end += index + 2
			for pos := index; pos < end; pos++ {
				codeBytes[pos] = ' '
			}
			index = end
			l.inBlockComment = false
			continue
		}
		if l.inRawString {
			end := strings.IndexByte(raw[index:], '`')
			if end < 0 {
				return string(codeBytes), string(maskBytes)
			}
			index += end + 1
			l.inRawString = false
			continue
		}
		switch {
		case strings.HasPrefix(raw[index:], "//"):
			for pos := index; pos < len(raw); pos++ {
				codeBytes[pos] = ' '
			}
			return string(codeBytes), string(maskBytes)
		case strings.HasPrefix(raw[index:], "/*"):
			codeBytes[index] = ' '
			codeBytes[index+1] = ' '
			index += 2
			l.inBlockComment = true
		case raw[index] == '`':
			maskBytes[index] = ' '
			index++
			l.inRawString = true
		case raw[index] == '"' || raw[index] == '\'':
			quote := raw[index]
			maskBytes[index] = ' '
			index++
			for index < len(raw) {
				char := raw[index]
				maskBytes[index] = ' '
				if char == '\\' && index+1 < len(raw) {
					maskBytes[index+1] = ' '
					index += 2
					continue
				}
				index++
				if char == quote {
					break
				}
			}
		default:
			maskBytes[index] = raw[index]
			index++
		}
	}
	return string(codeBytes), string(maskBytes)
}

type apiTypeDecl struct {
	EndLine  int
	Messages []IDLMessage
	Aliases  []IDLTypeAlias
}

var errAPITypeBlockIncomplete = errors.New("parse api: type declaration is not closed")

// apiScanTypeDeclarations captures every type declaration or grouped type
// block and converts it through go/parser. The returned map is keyed by the
// 1-based line where the declaration starts.
func apiScanTypeDeclarations(content, filename string) (map[int]apiTypeDecl, error) {
	rawLines := strings.Split(content, "\n")
	masks := make([]string, len(rawLines))
	lexer := &apiLineLexer{}
	for index, raw := range rawLines {
		raw = strings.TrimSuffix(raw, "\r")
		rawLines[index] = raw
		_, masks[index] = lexer.line(raw)
	}

	decls := make(map[int]apiTypeDecl)
	for index := 0; index < len(rawLines); index++ {
		if !apiTypeKeywordAtStart(masks[index]) {
			continue
		}
		grouped := apiTypeGroupStart(masks[index])
		startLine := index + 1
		captured := false
		for end := index; end < len(rawLines) && end-index < 1000; end++ {
			messages, aliases, err := apiParseTypeBlock(rawLines, masks, index, end, filename, grouped)
			if err == nil {
				decls[startLine] = apiTypeDecl{
					EndLine:  end + 1,
					Messages: messages,
					Aliases:  aliases,
				}
				index = end
				captured = true
				break
			}
			incomplete := errors.Is(err, errAPITypeBlockIncomplete)
			if end == len(rawLines)-1 {
				if incomplete {
					if grouped {
						return nil, errors.New("parse api: type block is not closed")
					}
					return nil, errors.New("parse api: type declaration is not closed")
				}
				return nil, err
			}
			if !incomplete && !apiGoParseErrorAtEOF(err) {
				return nil, err
			}
		}
		if !captured {
			return nil, errors.New("parse api: type declaration exceeds 1000 lines")
		}
	}
	return decls, nil
}

func apiTypeKeywordAtStart(mask string) bool {
	rest, ok := strings.CutPrefix(strings.TrimSpace(mask), "type")
	if !ok {
		return false
	}
	if rest == "" {
		return true
	}
	switch rest[0] {
	case ' ', '\t', '(':
		return true
	default:
		return false
	}
}

func apiTypeGroupStart(mask string) bool {
	rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(mask), "type"))
	return strings.HasPrefix(rest, "(")
}

func apiGoParseErrorAtEOF(err error) bool {
	return strings.Contains(err.Error(), "found 'EOF'") ||
		strings.Contains(err.Error(), "expected 'EOF'")
}

func apiParseTypeBlock(rawLines, masks []string, start, end int, filename string, grouped bool) ([]IDLMessage, []IDLTypeAlias, error) {
	lines := make([]string, 0, end-start+1)
	braceDepth := 0
	parenDepth := 0
	for index := start; index <= end; index++ {
		raw := apiSanitizeTypeDirective(rawLines[index])
		mask := masks[index]
		if apiTypeDeclarationContext(grouped, braceDepth, parenDepth) {
			if pos := apiTypeSugarBrace(mask, grouped); pos >= 0 {
				raw = raw[:pos] + "struct " + raw[pos:]
				mask = mask[:pos] + "struct " + mask[pos:]
			}
		}
		lines = append(lines, raw)
		braceDepth += strings.Count(mask, "{") - strings.Count(mask, "}")
		parenDepth += strings.Count(mask, "(") - strings.Count(mask, ")")
	}
	if grouped && parenDepth != 0 {
		return nil, nil, errAPITypeBlockIncomplete
	}
	if braceDepth != 0 {
		return nil, nil, errAPITypeBlockIncomplete
	}

	displayName := apiTypeFilename(filename)
	var source strings.Builder
	source.WriteString("package api\n\n")
	fmt.Fprintf(&source, "//line %s:%d\n", displayName, start+1)
	source.WriteString(strings.Join(lines, "\n"))
	source.WriteString("\n")

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, displayName, source.String(), parser.ParseComments|parser.AllErrors)
	if err != nil {
		return nil, nil, fmt.Errorf("parse api type: %w", err)
	}
	return apiDeclarationsFromFile(file, fset)
}

func apiSanitizeTypeDirective(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "//line ") || strings.HasPrefix(trimmed, "//go:") {
		return ""
	}
	return raw
}

func apiTypeDeclarationContext(grouped bool, braceDepth, parenDepth int) bool {
	if grouped {
		return braceDepth == 0 && parenDepth == 1
	}
	return braceDepth == 0
}

// apiTypeSugarBrace finds the struct-opening brace of a ".api sugar"
// declaration (type Foo { ... } or grouped Foo { ... }). It returns -1 when
// the line is not a sugar declaration.
func apiTypeSugarBrace(mask string, grouped bool) int {
	trimmed := strings.TrimSpace(mask)
	prefix := trimmed
	if !grouped {
		rest, ok := strings.CutPrefix(prefix, "type")
		if !ok {
			return -1
		}
		prefix = strings.TrimSpace(rest)
	}
	if strings.HasPrefix(prefix, "struct") || strings.HasPrefix(prefix, "=") {
		return -1
	}
	identEnd := 0
	for identEnd < len(prefix) {
		char := prefix[identEnd]
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' {
			identEnd++
			continue
		}
		break
	}
	if identEnd == 0 {
		return -1
	}
	rest := strings.TrimSpace(prefix[identEnd:])
	if !strings.HasPrefix(rest, "{") {
		return -1
	}
	// The first structural brace on the line belongs to the declaration for
	// both "type Foo {" and grouped "Foo {".
	pos := strings.IndexByte(mask, '{')
	if pos < 0 {
		return -1
	}
	return pos
}

func apiTypeFilename(filename string) string {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return "<api>"
	}
	filename = strings.ReplaceAll(filename, "\r", " ")
	filename = strings.ReplaceAll(filename, "\n", " ")
	filename = strings.ReplaceAll(filename, "\\", "/")
	return filename
}

func apiDeclarationsFromFile(file *ast.File, fset *token.FileSet) ([]IDLMessage, []IDLTypeAlias, error) {
	var messages []IDLMessage
	var aliases []IDLTypeAlias
	for _, declaration := range file.Decls {
		gen, ok := declaration.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, rawSpec := range gen.Specs {
			spec, ok := rawSpec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if spec.TypeParams != nil && len(spec.TypeParams.List) > 0 {
				return nil, nil, apiPositionError(fset, spec.Pos(), "generics are not supported in .api type declarations")
			}
			if structure, ok := spec.Type.(*ast.StructType); ok {
				fields, err := apiFieldsFromStruct(structure, fset)
				if err != nil {
					return nil, nil, err
				}
				messages = append(messages, IDLMessage{
					Name:   spec.Name.Name,
					Fields: fields,
					Alias:  spec.Assign.IsValid(),
				})
				continue
			}
			if err := apiValidateTypeExpr(spec.Type, fset); err != nil {
				return nil, nil, err
			}
			if apiExprContainsStruct(spec.Type) {
				return nil, nil, apiPositionError(fset, spec.Pos(), "anonymous struct aliases are only supported as direct struct declarations")
			}
			target, err := apiCanonicalType(spec.Type, fset)
			if err != nil {
				return nil, nil, err
			}
			aliases = append(aliases, IDLTypeAlias{
				Name:   spec.Name.Name,
				Target: target,
				Alias:  spec.Assign.IsValid(),
			})
		}
	}
	return messages, aliases, nil
}

func apiFieldsFromStruct(structure *ast.StructType, fset *token.FileSet) ([]IDLField, error) {
	fields := make([]IDLField, 0, len(structure.Fields.List))
	for _, field := range structure.Fields.List {
		tag, err := apiFieldTag(field, fset)
		if err != nil {
			return nil, err
		}
		if len(field.Names) == 0 {
			embedded, err := apiEmbeddedField(field.Type, tag, fset)
			if err != nil {
				return nil, err
			}
			fields = append(fields, embedded)
			continue
		}
		if len(field.Names) > 1 {
			if err := apiValidateMultiNameTag(field.Names, tag, fset, field.Pos()); err != nil {
				return nil, err
			}
		}
		nested, slice, mapKey, err := apiNestedField(field.Type, fset)
		if err != nil {
			return nil, err
		}
		rawType := ""
		if nested == nil {
			rawType, err = apiCanonicalType(field.Type, fset)
			if err != nil {
				return nil, err
			}
			if err := apiValidateTypeExpr(field.Type, fset); err != nil {
				return nil, err
			}
		} else {
			rawType = "struct{}"
			if slice {
				rawType = "[]struct{}"
			}
			if mapKey != "" {
				rawType = "map[" + mapKey + "]struct{}"
			}
		}
		for _, name := range field.Names {
			fields = append(fields, IDLField{
				Name:         name.Name,
				Type:         rawType,
				Tag:          tag,
				Nested:       nested,
				NestedSlice:  slice,
				NestedMapKey: mapKey,
			})
		}
	}
	return fields, nil
}

func apiFieldTag(field *ast.Field, fset *token.FileSet) (string, error) {
	if field.Tag == nil {
		return "", nil
	}
	value, err := strconv.Unquote(field.Tag.Value)
	if err != nil {
		return "", apiPositionError(fset, field.Tag.Pos(), "invalid field tag")
	}
	return value, nil
}

func apiEmbeddedField(expr ast.Expr, tag string, fset *token.FileSet) (IDLField, error) {
	base := expr
	if pointer, ok := expr.(*ast.StarExpr); ok {
		base = pointer.X
	}
	name := ""
	switch typed := base.(type) {
	case *ast.Ident:
		name = typed.Name
	case *ast.SelectorExpr:
		qualified, err := apiCanonicalType(typed, fset)
		if err != nil {
			return IDLField{}, err
		}
		if qualified != "time.Time" {
			return IDLField{}, apiPositionError(fset, expr.Pos(), "unsupported embedded field %s", qualified)
		}
		name = "Time"
	default:
		return IDLField{}, apiPositionError(fset, expr.Pos(), "invalid embedded field")
	}
	if err := apiValidateTypeExpr(expr, fset); err != nil {
		return IDLField{}, err
	}
	rawType, err := apiCanonicalType(expr, fset)
	if err != nil {
		return IDLField{}, err
	}
	return IDLField{Name: name, Type: rawType, Tag: tag, Inline: true}, nil
}

func apiNestedField(expr ast.Expr, fset *token.FileSet) (*IDLMessage, bool, string, error) {
	switch typed := expr.(type) {
	case *ast.StructType:
		nested, err := apiNestedMessage(typed, fset)
		return nested, false, "", err
	case *ast.ArrayType:
		if typed.Len != nil {
			return nil, false, "", apiPositionError(fset, expr.Pos(), "fixed-size array types are not supported in .api")
		}
		structure, ok := typed.Elt.(*ast.StructType)
		if !ok {
			return nil, false, "", nil
		}
		nested, err := apiNestedMessage(structure, fset)
		return nested, true, "", err
	case *ast.MapType:
		structure, ok := typed.Value.(*ast.StructType)
		if !ok {
			return nil, false, "", nil
		}
		key, err := apiCanonicalType(typed.Key, fset)
		if err != nil {
			return nil, false, "", err
		}
		if !isAPIMapKeyType(key) {
			return nil, false, "", apiPositionError(fset, typed.Key.Pos(), "unsupported map key type %s", key)
		}
		nested, err := apiNestedMessage(structure, fset)
		return nested, false, key, err
	default:
		return nil, false, "", nil
	}
}

func apiNestedMessage(structure *ast.StructType, fset *token.FileSet) (*IDLMessage, error) {
	fields, err := apiFieldsFromStruct(structure, fset)
	if err != nil {
		return nil, err
	}
	return &IDLMessage{Fields: fields}, nil
}

func apiValidateMultiNameTag(names []*ast.Ident, tag string, fset *token.FileSet, pos token.Pos) error {
	if key, value, unsafe := apiMultiNameTagConflict(tag); unsafe {
		fieldNames := make([]string, 0, len(names))
		for _, name := range names {
			fieldNames = append(fieldNames, name.Name)
		}
		return apiPositionError(
			fset,
			pos,
			"multi-name field %s must not share tag %s:%q",
			strings.Join(fieldNames, ", "),
			key,
			value,
		)
	}
	return nil
}

func apiMultiNameTagConflict(tag string) (string, string, bool) {
	if strings.TrimSpace(tag) == "" {
		return "", "", false
	}
	for _, key := range []string{"json", "form", "path", "header"} {
		value, ok := lookupAPIStructTag(tag, key)
		if !ok {
			continue
		}
		wireName := strings.Split(value, ",")[0]
		if wireName == "" || wireName == "-" {
			continue
		}
		return key, value, true
	}
	return "", "", false
}

func apiValidateTypeExpr(expr ast.Expr, fset *token.FileSet) error {
	switch typed := expr.(type) {
	case *ast.Ident:
		switch typed.Name {
		case "complex64", "complex128":
			return apiPositionError(fset, expr.Pos(), "unsupported Go type in .api: complex")
		}
		return nil
	case *ast.SelectorExpr:
		pkg, ok := typed.X.(*ast.Ident)
		if !ok {
			return apiPositionError(fset, expr.Pos(), "unsupported package-qualified type")
		}
		name := pkg.Name + "." + typed.Sel.Name
		if name == "time.Time" {
			return nil
		}
		if name == "unsafe.Pointer" {
			return apiPositionError(fset, expr.Pos(), "unsupported Go type in .api: unsafe.Pointer")
		}
		return apiPositionError(fset, expr.Pos(), "unsupported package-qualified type %s", name)
	case *ast.StarExpr:
		return apiValidateTypeExpr(typed.X, fset)
	case *ast.ArrayType:
		if typed.Len != nil {
			return apiPositionError(fset, expr.Pos(), "fixed-size array types are not supported in .api")
		}
		return apiValidateTypeExpr(typed.Elt, fset)
	case *ast.MapType:
		if err := apiValidateTypeExpr(typed.Key, fset); err != nil {
			return err
		}
		return apiValidateTypeExpr(typed.Value, fset)
	case *ast.InterfaceType:
		if typed.Methods != nil && len(typed.Methods.List) > 0 {
			return apiPositionError(fset, expr.Pos(), "non-empty interface types are not supported")
		}
		return nil
	case *ast.FuncType:
		return apiPositionError(fset, expr.Pos(), "unsupported Go type in .api: func")
	case *ast.ChanType:
		return apiPositionError(fset, expr.Pos(), "unsupported Go type in .api: chan")
	case *ast.ParenExpr:
		return apiValidateTypeExpr(typed.X, fset)
	case *ast.IndexExpr, *ast.IndexListExpr:
		return apiPositionError(fset, expr.Pos(), "generics are not supported in .api type declarations")
	case *ast.Ellipsis:
		return apiPositionError(fset, expr.Pos(), "variadic types are not supported in .api type declarations")
	case *ast.StructType:
		_, err := apiFieldsFromStruct(typed, fset)
		return err
	default:
		return apiPositionError(fset, expr.Pos(), "unsupported Go type in .api")
	}
}

func apiExprContainsStruct(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		if _, ok := node.(*ast.StructType); ok {
			found = true
			return false
		}
		return true
	})
	return found
}

func apiCanonicalType(expr ast.Expr, fset *token.FileSet) (string, error) {
	var buffer strings.Builder
	if err := printer.Fprint(&buffer, fset, expr); err != nil {
		return "", apiPositionError(fset, expr.Pos(), "render type expression: %v", err)
	}
	rendered := strings.Join(strings.Fields(buffer.String()), " ")
	if rendered == "" || strings.ContainsAny(rendered, "\n\r\t`") {
		return "", apiPositionError(fset, expr.Pos(), "unsupported multi-line type expression")
	}
	return rendered, nil
}

func apiPositionError(fset *token.FileSet, pos token.Pos, format string, args ...any) error {
	message := fmt.Sprintf(format, args...)
	if position := fset.Position(pos); position.IsValid() {
		return fmt.Errorf("%s: %s", position, message)
	}
	return errors.New(message)
}
