package generator

import "strings"

// apiDocumentWithResolvedInlineFields returns a copy whose message fields are
// flattened through anonymous embedded API types. It is used only by schema and
// non-Go client generators; Go DTO generation keeps the original embedding.
func apiDocumentWithResolvedInlineFields(doc IDLDocument) IDLDocument {
	messages := make(map[string]IDLMessage, len(doc.Messages))
	for _, msg := range doc.Messages {
		messages[exportName(msg.Name)] = msg
	}
	for index := range doc.Messages {
		doc.Messages[index].Fields = resolveAPIInlineFields(doc.Messages[index], messages, map[string]bool{})
	}
	return doc
}

func resolveAPIInlineFields(msg IDLMessage, messages map[string]IDLMessage, resolving map[string]bool) []IDLField {
	name := exportName(msg.Name)
	if resolving[name] {
		return nil
	}
	resolving[name] = true
	defer delete(resolving, name)

	explicit := make(map[string]struct{}, len(msg.Fields))
	for _, field := range msg.Fields {
		if !field.Inline {
			explicit[apiInlineFieldKey(field)] = struct{}{}
		}
	}
	seen := make(map[string]struct{}, len(msg.Fields))
	fields := make([]IDLField, 0, len(msg.Fields))
	for _, field := range msg.Fields {
		if !field.Inline {
			key := apiInlineFieldKey(field)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			fields = append(fields, field)
			continue
		}
		embedded, ok := messages[exportName(apiBaseType(field.Type))]
		if !ok || resolving[exportName(embedded.Name)] {
			continue
		}
		for _, promoted := range resolveAPIInlineFields(embedded, messages, resolving) {
			key := apiInlineFieldKey(promoted)
			if _, overridden := explicit[key]; overridden {
				continue
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			fields = append(fields, promoted)
		}
	}
	return fields
}

func apiInlineFieldKey(field IDLField) string {
	if field.Tag != "" {
		for _, key := range []string{"json:\"", "path:\"", "form:\""} {
			if index := strings.Index(field.Tag, key); index >= 0 {
				value := field.Tag[index+len(key):]
				if end := strings.IndexByte(value, '"'); end >= 0 {
					if name := strings.Split(value[:end], ",")[0]; name != "" {
						return strings.ToLower(name)
					}
				}
			}
		}
	}
	return strings.ToLower(lowerCamel(field.Name))
}
