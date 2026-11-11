package generator

type IDLDocument struct {
	Kind           string
	Syntax         string
	Package        string
	GoPackage      string
	Imports        []string
	ImportedProtos []IDLImportedProto
	Messages       []IDLMessage
	// Aliases holds non-struct type declarations such as "type UserID int64"
	// and "type RawJSON = any". Named struct declarations, including struct
	// aliases, live in Messages with IDLMessage.Alias set so every existing
	// message consumer sees them.
	Aliases  []IDLTypeAlias
	Enums    []IDLEnum
	Services []IDLService
}

// IDLTypeAlias describes a non-struct .api type declaration. Alias reports
// whether the declaration used "=" (a Go alias) or a plain defined type.
type IDLTypeAlias struct {
	Name   string
	Target string // canonical single-line Go type expression
	Alias  bool
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
	// Alias reports that the declaration used "type X = struct{...}".
	Alias bool
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
	// Nested carries an inline anonymous struct payload. Type holds a
	// single-line placeholder (struct{}, []struct{}, map[K]struct{}) for display
	// and diff purposes; schema and code generators must inspect Nested first.
	Nested       *IDLMessage
	NestedSlice  bool
	NestedMapKey string
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

type IDLRouteAnnotation struct {
	Handler string
	Group   string
	Values  map[string]string
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
	Route         IDLRouteAnnotation
}
