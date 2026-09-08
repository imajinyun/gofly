package generator

type IDLDocument struct {
	Kind           string
	Package        string
	GoPackage      string
	Imports        []string
	ImportedProtos []IDLImportedProto
	Messages       []IDLMessage
	Enums          []IDLEnum
	Services       []IDLService
}

// IDLImportedProto describes a locally resolved imported Proto package used by
// generated RPC method signatures.
type IDLImportedProto struct {
	ProtoPackage string
	GoPackage    string
	Alias        string
}

type IDLEnum struct {
	Name   string
	Values []IDLEnumValue
}

type IDLEnumValue struct {
	Name   string
	Number int
}

type IDLMessage struct {
	Name   string
	Fields []IDLField
}

type IDLField struct {
	Name   string
	Type   string
	Tag    string
	Number int
	// Inline reports that the field is an anonymous embedded API type. Inline
	// fields are preserved as Go embeddings and flattened only by consumers
	// whose schema formats do not support Go field promotion.
	Inline bool
}

type IDLService struct {
	Name    string
	Server  IDLServerAnnotation
	Methods []IDLMethod
}

type IDLServerAnnotation struct {
	Group      string
	Prefix     string
	JWT        string
	Middleware []string
	Values     map[string]string
}

type IDLMethod struct {
	Name          string
	Request       string
	Response      string
	ProtoRequest  string
	ProtoResponse string
	ClientStream  bool
	ServerStream  bool
	HTTPMethod    string
	HTTPPath      string
	Handler       string
	Doc           map[string]string
}
