package migration

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// sqlTokens separates syntax from quoted text and comments. It is intentionally
// not a model parser: column definitions and constraints are never reconstructed.
func sqlTokens(sql string) ([]string, error) {
	var tokens []string
	for i := 0; i < len(sql); {
		c := sql[i]
		if c <= ' ' {
			i++
			continue
		}
		if strings.HasPrefix(sql[i:], "--") || c == '#' {
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
			continue
		}
		if strings.HasPrefix(sql[i:], "/*") {
			if strings.HasPrefix(sql[i:], "/*!") || strings.HasPrefix(sql[i:], "/*M!") {
				return nil, errors.New("executable SQL comments are unsupported")
			}
			end := strings.Index(sql[i+2:], "*/")
			if end < 0 {
				return nil, errors.New("unclosed SQL comment")
			}
			i += end + 4
			continue
		}
		start := i
		if c == '\'' || c == '"' || c == '`' {
			i++
			closed := false
			for i < len(sql) {
				if sql[i] == '\\' {
					return nil, errors.New("backslash-escaped quoted SQL is ambiguous across dialects; use doubled quotes")
				}
				if sql[i] == c {
					i++
					if i < len(sql) && sql[i] == c {
						i++
						continue
					}
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, errors.New("unclosed SQL quote")
			}
			tokens = append(tokens, sql[start:i])
			continue
		}
		if c == '$' {
			j := i + 1
			for j < len(sql) && (sql[j] == '_' || sql[j] >= 'a' && sql[j] <= 'z' || sql[j] >= 'A' && sql[j] <= 'Z' || sql[j] >= '0' && sql[j] <= '9') {
				j++
			}
			if j < len(sql) && sql[j] == '$' {
				tag := sql[i : j+1]
				end := strings.Index(sql[j+1:], tag)
				if end < 0 {
					return nil, errors.New("unclosed SQL dollar quote")
				}
				i = j + 1 + end + len(tag)
				tokens = append(tokens, sql[start:i])
				continue
			}
		}
		if strings.ContainsRune("();.,", rune(c)) {
			tokens = append(tokens, string(c))
			i++
			continue
		}
		for i < len(sql) && sql[i] > ' ' && !strings.ContainsRune("();.,'\"`", rune(sql[i])) {
			i++
		}
		if i == start {
			i++
		}
		tokens = append(tokens, sql[start:i])
	}
	return tokens, nil
}

func initialDown(up []byte, dialect string) ([]byte, error) {
	tokens, err := sqlTokens(string(up))
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return nil, errors.New("initial DDL must contain CREATE TABLE statements")
	}
	var tables []string
	seen := map[string]bool{}
	for len(tokens) > 0 {
		if len(tokens) < 5 || !strings.EqualFold(tokens[0], "CREATE") || !strings.EqualFold(tokens[1], "TABLE") {
			return nil, errors.New("initial DDL only supports unconditional CREATE TABLE statements")
		}
		name := tokens[2]
		i := 3
		if !tableIdent(name, dialect) {
			return nil, errors.New("unsupported table identifier")
		}
		if i+1 < len(tokens) && tokens[i] == "." {
			if !tableIdent(tokens[i+1], dialect) {
				return nil, errors.New("unsupported qualified table identifier")
			}
			name += "." + tokens[i+1]
			i += 2
		}
		if i >= len(tokens) || tokens[i] != "(" {
			return nil, errors.New("initial DDL requires CREATE TABLE name (...) without IF NOT EXISTS or AS SELECT")
		}
		key := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(name, "`", ""), "\"", ""))
		last := key[strings.LastIndex(key, ".")+1:]
		if last == "schema_migrations" || last == "gofly_migration_checksums" {
			return nil, errors.New("DDL cannot create migration metadata tables")
		}
		if seen[key] {
			return nil, errors.New("duplicate table in initial DDL")
		}
		seen[key] = true
		depth := 0
		end := -1
		for j := i; j < len(tokens); j++ {
			switch tokens[j] {
			case "(":
				depth++
			case ")":
				depth--
				if depth < 0 {
					return nil, errors.New("unbalanced DDL parentheses")
				}
			case ";":
				if depth != 0 {
					return nil, errors.New("unexpected semicolon inside table definition")
				}
				end = j
			}
			if end >= 0 {
				break
			}
		}
		if depth != 0 {
			return nil, errors.New("unbalanced DDL parentheses")
		}
		if end < 0 {
			end = len(tokens)
		}
		// CREATE TABLE AS/LIKE can populate or copy a table and is outside initial-DDL generation.
		for _, token := range tokens[i:end] {
			if strings.EqualFold(token, "SELECT") {
				return nil, errors.New("CREATE TABLE AS SELECT is unsupported")
			}
		}
		tables = append(tables, name)
		if end == len(tokens) {
			tokens = nil
		} else {
			tokens = tokens[end+1:]
		}
	}
	var down strings.Builder
	down.WriteString("-- Rollback drops these tables and their data.\n")
	for i := len(tables) - 1; i >= 0; i-- {
		fmt.Fprintf(&down, "DROP TABLE %s;\n", tables[i])
	}
	return []byte(down.String()), nil
}

func tableIdent(value, dialect string) bool {
	if value == "" {
		return false
	}
	quote := byte('"')
	if dialect == "mysql" {
		quote = '`'
	}
	if value[0] == quote {
		return len(value) > 2 && value[len(value)-1] == quote
	}
	for i, r := range value {
		if r != '_' && !unicode.IsLetter(r) && (i == 0 || !unicode.IsDigit(r)) {
			return false
		}
	}
	return true
}
