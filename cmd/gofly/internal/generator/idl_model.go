package generator

type IDLDocument struct {
	Kind      string
	Package   string
	GoPackage string
	Imports   []string
	Messages  []IDLMessage
	Enums     []IDLEnum
	Services  []IDLService
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
	Name         string
	Request      string
	Response     string
	ClientStream bool
	ServerStream bool
	HTTPMethod   string
	HTTPPath     string
	Handler      string
}
