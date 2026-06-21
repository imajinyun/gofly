package generator

import (
	"bufio"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	protoPackageRE   = regexp.MustCompile(`^package\s+([A-Za-z_][A-Za-z0-9_.]*)\s*;`)
	protoImportRE    = regexp.MustCompile(`^import\s+(?:public\s+|weak\s+)?"([^"]+)"\s*;`)
	protoGoPackageRE = regexp.MustCompile(`^option\s+go_package\s*=\s*"([^"]+)"\s*;`)
	protoMessageRE   = regexp.MustCompile(`^message\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{`)
	protoEnumRE      = regexp.MustCompile(`^enum\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{`)
	protoServiceRE   = regexp.MustCompile(`^service\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{`)
	protoFieldRE     = regexp.MustCompile(`^((?:optional|repeated)\s+)?([A-Za-z_][A-Za-z0-9_.<>]*(?:<\s*[A-Za-z_][A-Za-z0-9_.]*\s*,\s*[A-Za-z_][A-Za-z0-9_.]*\s*>)?)\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*([0-9]+)\s*(?:\[[^\]]+\])?\s*;`)
	protoEnumValueRE = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(-?[0-9]+)\s*(?:\[[^\]]+\])?\s*;`)
	protoRPCRE       = regexp.MustCompile(`^rpc\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(\s*(stream\s+)?([A-Za-z_][A-Za-z0-9_.]*)\s*\)\s*returns\s*\(\s*(stream\s+)?([A-Za-z_][A-Za-z0-9_.]*)\s*\)\s*(?:;|\{)?$`)
	protoHTTPRuleRE  = regexp.MustCompile(`^(get|post|put|patch|delete)\s*:\s*"([^"]+)"`)
	protoBlockRE     = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

func ParseProto(content string) (IDLDocument, error) {
	doc := IDLDocument{Kind: "proto"}
	scanner := bufio.NewScanner(strings.NewReader(expandProtoInlineBlocks(stripBlockComments(content))))
	var currentMessage *IDLMessage
	var currentEnum *IDLEnum
	var currentService *IDLService
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(stripLineComment(scanner.Text()))
		if line == "" {
			continue
		}
		if currentMessage != nil {
			if line == "}" || line == "};" {
				doc.Messages = append(doc.Messages, *currentMessage)
				currentMessage = nil
				continue
			}
			match := protoFieldRE.FindStringSubmatch(line)
			if match == nil {
				if isIgnorableProtoMessageLine(line) {
					continue
				}
				return IDLDocument{}, fmt.Errorf("parse proto line %d: invalid field", lineNo)
			}
			number, err := strconv.Atoi(match[4])
			if err != nil {
				return IDLDocument{}, fmt.Errorf("parse proto line %d: invalid field number: %w", lineNo, err)
			}
			fieldType := match[2]
			if strings.TrimSpace(match[1]) == "repeated" {
				fieldType = "repeated " + fieldType
			}
			currentMessage.Fields = append(currentMessage.Fields, IDLField{Name: match[3], Type: fieldType, Number: number})
			continue
		}
		if currentEnum != nil {
			if line == "}" || line == "};" {
				doc.Enums = append(doc.Enums, *currentEnum)
				currentEnum = nil
				continue
			}
			match := protoEnumValueRE.FindStringSubmatch(line)
			if match == nil {
				return IDLDocument{}, fmt.Errorf("parse proto line %d: invalid enum value", lineNo)
			}
			number, err := strconv.Atoi(match[2])
			if err != nil {
				return IDLDocument{}, fmt.Errorf("parse proto line %d: invalid enum value number: %w", lineNo, err)
			}
			currentEnum.Values = append(currentEnum.Values, IDLEnumValue{Name: match[1], Number: number})
			continue
		}
		if currentService != nil {
			if line == "}" || line == "};" {
				doc.Services = append(doc.Services, *currentService)
				currentService = nil
				continue
			}
			if line == ";" {
				continue
			}
			match := protoRPCRE.FindStringSubmatch(line)
			if match == nil {
				return IDLDocument{}, fmt.Errorf("parse proto line %d: invalid rpc method", lineNo)
			}
			method := IDLMethod{
				Name:         match[1],
				ClientStream: strings.TrimSpace(match[2]) == "stream",
				Request:      lastIdent(match[3]),
				ServerStream: strings.TrimSpace(match[4]) == "stream",
				Response:     lastIdent(match[5]),
			}
			if !strings.HasSuffix(line, ";") {
				if err := readProtoRPCOptions(scanner, &lineNo, &method, strings.HasSuffix(line, "{")); err != nil {
					return IDLDocument{}, err
				}
			}
			currentService.Methods = append(currentService.Methods, method)
			continue
		}
		if match := protoImportRE.FindStringSubmatch(line); match != nil {
			doc.Imports = append(doc.Imports, match[1])
			continue
		}
		if match := protoPackageRE.FindStringSubmatch(line); match != nil {
			doc.Package = match[1]
			continue
		}
		if match := protoGoPackageRE.FindStringSubmatch(line); match != nil {
			doc.GoPackage = match[1]
			continue
		}
		if isIgnorableProtoTopLevelLine(line) {
			continue
		}
		if match := protoMessageRE.FindStringSubmatch(line); match != nil {
			currentMessage = &IDLMessage{Name: match[1]}
			continue
		}
		if match := protoEnumRE.FindStringSubmatch(line); match != nil {
			currentEnum = &IDLEnum{Name: match[1]}
			continue
		}
		if match := protoServiceRE.FindStringSubmatch(line); match != nil {
			currentService = &IDLService{Name: match[1]}
			continue
		}
	}
	if err := scanner.Err(); err != nil {
		return IDLDocument{}, fmt.Errorf("scan proto: %w", err)
	}
	if currentMessage != nil {
		return IDLDocument{}, fmt.Errorf("parse proto: message %s is not closed", currentMessage.Name)
	}
	if currentEnum != nil {
		return IDLDocument{}, fmt.Errorf("parse proto: enum %s is not closed", currentEnum.Name)
	}
	if currentService != nil {
		return IDLDocument{}, fmt.Errorf("parse proto: service %s is not closed", currentService.Name)
	}
	if len(doc.Services) == 0 && len(doc.Messages) == 0 && len(doc.Enums) == 0 {
		return IDLDocument{}, fmt.Errorf("parse proto: no message or service found")
	}
	return doc, nil
}

func readProtoRPCOptions(scanner *bufio.Scanner, lineNo *int, method *IDLMethod, opened bool) error {
	depth := 0
	started := false
	if opened {
		depth = 1
		started = true
	}
	for scanner.Scan() {
		*lineNo = *lineNo + 1
		line := strings.TrimSpace(stripLineComment(scanner.Text()))
		if line == "" {
			continue
		}
		if line == "{" || strings.HasSuffix(line, "{") {
			depth++
			started = true
		}
		if match := protoHTTPRuleRE.FindStringSubmatch(line); match != nil {
			method.HTTPMethod = strings.ToUpper(match[1])
			method.HTTPPath = match[2]
		}
		if strings.HasSuffix(line, "{") {
			continue
		}
		if line == "}" || line == "};" {
			if depth > 0 {
				depth--
			}
			if started && depth == 0 {
				return nil
			}
			continue
		}
		if !started {
			return fmt.Errorf("parse proto line %d: rpc method options must start with block", *lineNo)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan proto rpc options: %w", err)
	}
	return fmt.Errorf("parse proto: rpc method %s option block is not closed", method.Name)
}

func isIgnorableProtoMessageLine(line string) bool {
	line = strings.TrimSpace(line)
	return strings.HasPrefix(line, "option ") ||
		strings.HasPrefix(line, "reserved ") ||
		strings.HasPrefix(line, "extensions ")
}

func isIgnorableProtoTopLevelLine(line string) bool {
	line = strings.TrimSpace(line)
	return strings.HasPrefix(line, "syntax ") ||
		strings.HasPrefix(line, "option ") ||
		strings.HasPrefix(line, "reserved ")
}

func expandProtoInlineBlocks(s string) string {
	var b strings.Builder
	inString := false
	escaped := false
	for _, r := range s {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && inString {
			b.WriteRune(r)
			escaped = true
			continue
		}
		if r == '"' {
			b.WriteRune(r)
			inString = !inString
			continue
		}
		if inString {
			b.WriteRune(r)
			continue
		}
		switch r {
		case '{':
			b.WriteRune(r)
			b.WriteByte('\n')
		case '}':
			b.WriteByte('\n')
			b.WriteRune(r)
			b.WriteByte('\n')
		case ';':
			b.WriteRune(r)
			b.WriteByte('\n')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func stripBlockComments(s string) string {
	return protoBlockRE.ReplaceAllString(s, "")
}

func stripLineComment(s string) string {
	if idx := strings.Index(s, "//"); idx >= 0 {
		return s[:idx]
	}
	return s
}

func lastIdent(s string) string {
	parts := strings.Split(s, ".")
	return parts[len(parts)-1]
}
