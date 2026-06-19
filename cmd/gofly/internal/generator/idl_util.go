package generator

import (
	"fmt"
	"io"
	"strings"
	"unicode"
)

func exportName(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == '_' || r == '-' || r == '.' || r == '/'
	})
	var b strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		runes := []rune(part)
		runes[0] = unicode.ToUpper(runes[0])
		b.WriteString(string(runes))
	}
	if b.Len() == 0 {
		return "X"
	}
	return b.String()
}

func lowerCamel(s string) string {
	exported := exportName(s)
	runes := []rune(exported)
	if len(runes) == 0 {
		return ""
	}
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

func writeDefensiveHandlerBinding(b io.Writer, index int, methodName string, requestName string) {
	fprintf(b, "\tdesc.Methods[%d].Handler = func(ctx context.Context, req any) (any, error) {\n", index)
	fprintf(b, "\t\ttyped, ok := req.(*%s)\n", requestName)
	fprintf(b, "\t\tif !ok || typed == nil {\n")
	fprintf(b, "\t\t\treturn nil, rpc.NewError(rpc.CodeInvalidArgument, %q)\n", "unexpected request type for "+methodName)
	fprintf(b, "\t\t}\n")
	fprintf(b, "\t\treturn impl.%s(ctx, typed)\n", methodName)
	fprintf(b, "\t}\n")
}

func fprintf(b io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(b, format, args...)
}
