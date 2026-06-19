package generator

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// HandlerCompleter 负责把新增的 handler 方法追加到现有 Go 文件。
// 它通过 AST 识别已有方法，只写入缺失的部分。
type HandlerCompleter struct {
	// Receiver 是目标 handler 的接收者名称（用于在 AST 中匹配方法）。
	Receiver string
	// Package 是生成文件时使用的 package 名。
	Package string
	// Imports 是默认导入列表，如 "github.com/gofly/gofly/rest"。
	Imports []string
	// File 是目标文件路径（绝对或相对当前工作目录）。
	File string
}

// NewHandlerCompleter 构造一个 Completer；参数是文件路径与接收者（如果为空会自动检测）。
func NewHandlerCompleter(file, receiver, pkg string, imports []string) *HandlerCompleter {
	return &HandlerCompleter{File: file, Receiver: receiver, Package: pkg, Imports: imports}
}

// Method 描述一个待生成的 handler 方法。
type Method struct {
	// Name 是方法名。
	Name string
	// Body 是方法体内部的 Go 代码（首行缩进 1 tab）。
	Body string
	// Comment 是方法前的注释（可选）。
	Comment string
	// Signature 是完整的函数签名（包含 func 关键字等）；当为空时会由接收器和名称自动生成一个占位签名。
	Signature string
}

type HandlerCompleteOptions struct {
	File     string
	IDLFile  string
	Receiver string
	Package  string
	Imports  []string
}

func CompleteHandlersFromIDL(opts HandlerCompleteOptions) (int, error) {
	if strings.TrimSpace(opts.IDLFile) == "" {
		return 0, errors.New("idl file is required")
	}
	doc, err := readCompleterIDL(opts.IDLFile)
	if err != nil {
		return 0, err
	}
	methods := methodsFromIDLDocument(doc)
	return NewHandlerCompleter(opts.File, opts.Receiver, opts.Package, opts.Imports).Complete(methods)
}

func readCompleterIDL(path string) (IDLDocument, error) {
	// #nosec G304 -- handler completion reads an explicit API/proto/thrift IDL file supplied by the caller.
	data, err := os.ReadFile(path)
	if err != nil {
		return IDLDocument{}, fmt.Errorf("read idl file: %w", err)
	}
	if strings.EqualFold(filepath.Ext(path), ".api") {
		return ParseAPI(string(data))
	}
	return ReadRPCIDL(path)
}

func methodsFromIDLDocument(doc IDLDocument) []Method {
	out := []Method{}
	for _, svc := range doc.Services {
		for _, method := range svc.Methods {
			name := method.Handler
			if name == "" {
				name = method.Name
			}
			if name == "" {
				continue
			}
			comment := fmt.Sprintf("%s handles %s.%s.", exportName(name), exportName(svc.Name), exportName(method.Name))
			body := "\t// TODO: implement generated handler\n"
			if method.HTTPMethod != "" || method.HTTPPath != "" {
				body = fmt.Sprintf("\t// TODO: implement %s %s\n", strings.ToUpper(method.HTTPMethod), method.HTTPPath)
			}
			out = append(out, Method{Name: name, Comment: comment, Body: body})
		}
	}
	return out
}

// ExistingMethods 返回文件中已存在的方法名称（按名称去重）。
func (c *HandlerCompleter) ExistingMethods() ([]string, error) {
	if c.File == "" {
		return nil, errors.New("file is required")
	}
	data, err := os.ReadFile(c.File)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read file: %w", err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, c.File, data, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse file: %w", err)
	}
	out := []string{}
	seen := map[string]struct{}{}
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fd.Recv != nil && len(fd.Recv.List) > 0 {
			name := fd.Name.Name
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, name)
		}
	}
	return out, nil
}

// Complete 把缺失的 methods 追加到文件末尾；返回实际新增的方法数。
// 如果文件不存在则以 Package + imports 为头创建一个新文件。
func (c *HandlerCompleter) Complete(methods []Method) (int, error) {
	if len(methods) == 0 {
		return 0, nil
	}
	existing, err := c.ExistingMethods()
	if err != nil {
		return 0, err
	}
	seen := map[string]struct{}{}
	for _, m := range existing {
		seen[m] = struct{}{}
	}
	pending := make([]Method, 0, len(methods))
	for _, m := range methods {
		if m.Name == "" {
			continue
		}
		if _, ok := seen[m.Name]; ok {
			continue
		}
		seen[m.Name] = struct{}{}
		pending = append(pending, m)
	}
	if len(pending) == 0 {
		return 0, nil
	}

	if _, err := os.Stat(c.File); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return c.createNewFile(pending)
		}
		return 0, err
	}

	return c.appendMethods(pending)
}

func (c *HandlerCompleter) createNewFile(methods []Method) (int, error) {
	var buf bytes.Buffer
	pkg := c.Package
	if pkg == "" {
		pkg = "handler"
	}
	fmt.Fprintf(&buf, "package %s\n\n", pkg)
	if len(c.Imports) > 0 {
		buf.WriteString("import (\n")
		for _, imp := range c.Imports {
			fmt.Fprintf(&buf, "\t%q\n", imp)
		}
		buf.WriteString(")\n\n")
	}
	receiver := c.Receiver
	if receiver == "" {
		receiver = "h"
	}
	receiverType := exportName(receiver)
	for i, m := range methods {
		if m.Comment != "" {
			buf.WriteString(formatComment(m.Comment))
			buf.WriteString("\n")
		}
		if m.Signature != "" {
			buf.WriteString(m.Signature)
		} else {
			fmt.Fprintf(&buf, "func (%s *%s) %s() {\n", receiver, receiverType, exportName(m.Name))
		}
		if m.Body == "" {
			buf.WriteString("\t// TODO: implement\n")
		} else {
			body := strings.TrimRight(m.Body, "\n")
			buf.WriteString(body)
			buf.WriteString("\n")
		}
		buf.WriteString("}\n")
		if i < len(methods)-1 {
			buf.WriteString("\n")
		}
	}
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		// 如果格式化失败，仍然尝试写入原始内容，便于调试。
		if err := writeGeneratedFile(c.File, buf.Bytes()); err != nil {
			return 0, err
		}
		return len(methods), nil
	}
	if err := writeGeneratedFile(c.File, formatted); err != nil {
		return 0, err
	}
	return len(methods), nil
}

func (c *HandlerCompleter) appendMethods(methods []Method) (int, error) {
	f, err := os.OpenFile(c.File, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return 0, fmt.Errorf("open file for append: %w", err)
	}
	var written bytes.Buffer
	receiver := c.Receiver
	if receiver == "" {
		receiver = "h"
	}
	receiverType := exportName(receiver)
	for i, m := range methods {
		written.WriteString("\n\n")
		if m.Comment != "" {
			written.WriteString(formatComment(m.Comment))
			written.WriteString("\n")
		}
		if m.Signature != "" {
			written.WriteString(m.Signature)
		} else {
			fmt.Fprintf(&written, "func (%s *%s) %s() {\n", receiver, receiverType, exportName(m.Name))
		}
		if m.Body == "" {
			written.WriteString("\t// TODO: implement\n")
		} else {
			body := strings.TrimRight(m.Body, "\n")
			written.WriteString(body)
			written.WriteString("\n")
		}
		written.WriteString("}")
		_ = i
	}
	if _, err := f.Write(written.Bytes()); err != nil {
		_ = f.Close()
		return 0, err
	}
	if err := f.Close(); err != nil {
		return 0, err
	}
	return len(methods), nil
}

// formatComment 将多行字符串格式化为 Go 注释。
func formatComment(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("// ")
		b.WriteString(strings.TrimSpace(line))
	}
	return b.String()
}

// ExtractMethodSignature 通过一个简单的正则返回形如 "func (h *MyHandler) Foo()" 的签名；
// 若格式不符合要求则返回空字符串。
func ExtractMethodSignature(src string) string {
	src = strings.TrimSpace(src)
	if !strings.HasPrefix(src, "func ") {
		return ""
	}
	// 找到第一对匹配的圆括号或花括号。
	end := strings.Index(src, "{")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(src[:end])
}

// RenderMethodBlock 用于渲染一个带有固定模板的 handler 方法块（无 receiver 的普通函数）。
func RenderMethodBlock(name, body, comment string) string {
	var b strings.Builder
	if comment != "" {
		b.WriteString(formatComment(comment))
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "func %s() {\n", exportName(name))
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("}\n")
	return b.String()
}

// printAST 用于调试（保留在包中供开发者使用）。
func printAST(fset *token.FileSet, node ast.Node) string {
	var buf bytes.Buffer
	_ = printer.Fprint(&buf, fset, node)
	return buf.String()
}

// identifierRE 校验标识符是否合法，用于防止路径穿越等。
var identifierRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidIdentifier 返回字符串是否是合法的 Go 标识符。
func ValidIdentifier(s string) bool { return identifierRE.MatchString(s) }
