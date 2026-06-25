package generator

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/imajinyun/gofly/rpc"
)

// ChangeSeverity 表示变更对下游消费者的影响等级。
//
//   - SeverityBreaking：可能使既有客户端/调用者失败，CI 应失败
//   - SeverityWarning：可能是设计问题但未直接破坏协议（新增字段、默认值变更等）
//   - SeverityInfo：纯补充信息，不影响兼容性
type ChangeSeverity string

const (
	SeverityBreaking ChangeSeverity = "breaking"
	SeverityWarning  ChangeSeverity = "warning"
	SeverityInfo     ChangeSeverity = "info"
)

// ChangeCategory 粗粒度分类，方便按模块过滤。
type ChangeCategory string

const (
	CategoryRoute     ChangeCategory = "route"
	CategoryService   ChangeCategory = "service"
	CategoryMethod    ChangeCategory = "method"
	CategoryStream    ChangeCategory = "stream"
	CategoryType      ChangeCategory = "type"
	CategoryField     ChangeCategory = "field"
	CategoryEnum      ChangeCategory = "enum"
	CategoryPackage   ChangeCategory = "package"
	CategorySignature ChangeCategory = "signature"
)

// BreakingChange 描述一条契约变更。
type BreakingChange struct {
	Category    ChangeCategory `json:"category"`
	Severity    ChangeSeverity `json:"severity"`
	Subject     string         `json:"subject"`     // 变更对象的人类可读标识，例如 "GET /users/{id}"
	Description string         `json:"description"` // 详细说明，例如 "返回类型从 User 变更为 UserV2"
}

// BreakingChangesReport 聚合一次检测的结果。
type BreakingChangesReport struct {
	Changes  []BreakingChange `json:"changes"`
	Breaking int              `json:"breaking"`
	Warnings int              `json:"warnings"`
}

// IsEmpty 判断是否没有任何变更（信息性、告警、破坏性都算）。
func (r BreakingChangesReport) IsEmpty() bool { return len(r.Changes) == 0 }

// HasBreaking 是否包含至少一条 SeverityBreaking 级别的变更。
func (r BreakingChangesReport) HasBreaking() bool { return r.Breaking > 0 }

// APIBreakingOptions 控制 `gofly api breaking` 的检测粒度。
type APIBreakingOptions struct {
	Base   string // 基准 .api 文件路径
	Target string // 目标 .api 文件路径
}

// ProtoBreakingOptions 控制 `gofly rpc breaking` 的检测粒度。
type ProtoBreakingOptions struct {
	Base   string // 基准 .proto 文件路径
	Target string // 目标 .proto 文件路径
}

// ErrBreakingChanges 当检测到破坏性变更时由命令层返回，用于控制 exit code。
var ErrBreakingChanges = errors.New("breaking changes detected")

// DetectAPIChanges 比对两个 .api 文件，返回结构化变更报告。
// 仅返回实际存在的变更；无变更时 Changes 为空。
func DetectAPIChanges(opts APIBreakingOptions) (BreakingChangesReport, error) {
	if opts.Base == "" || opts.Target == "" {
		return BreakingChangesReport{}, errors.New("base and target api files are required")
	}
	base, err := readAPIFile(opts.Base)
	if err != nil {
		return BreakingChangesReport{}, fmt.Errorf("read base api: %w", err)
	}
	target, err := readAPIFile(opts.Target)
	if err != nil {
		return BreakingChangesReport{}, fmt.Errorf("read target api: %w", err)
	}
	return reportIDLChanges(base, target), nil
}

// DetectProtoChanges 比对两个 .proto 文件；当解析失败时保留错误。
func DetectProtoChanges(opts ProtoBreakingOptions) (BreakingChangesReport, error) {
	report, err := DetectProtoDescriptorChanges(opts)
	if err != nil {
		return BreakingChangesReport{}, err
	}
	return descriptorCompatibilityToBreakingReport(report), nil
}

// DetectProtoDescriptorChanges 比对两个 .proto 文件，并复用 rpc.Descriptor 的运行时契约规则。
func DetectProtoDescriptorChanges(opts ProtoBreakingOptions) (rpc.DescriptorCompatibilityReport, error) {
	if opts.Base == "" || opts.Target == "" {
		return rpc.DescriptorCompatibilityReport{}, errors.New("base and target proto files are required")
	}
	base, err := readProtoFile(opts.Base)
	if err != nil {
		return rpc.DescriptorCompatibilityReport{}, fmt.Errorf("read base proto: %w", err)
	}
	target, err := readProtoFile(opts.Target)
	if err != nil {
		return rpc.DescriptorCompatibilityReport{}, fmt.Errorf("read target proto: %w", err)
	}
	return reportProtoDescriptorChanges(base, target), nil
}

func readProtoFile(path string) (IDLDocument, error) {
	// #nosec G304 -- proto breaking checks compare explicit baseline/target proto file paths.
	content, err := os.ReadFile(path)
	if err != nil {
		return IDLDocument{}, err
	}
	return ParseProto(string(content))
}

func reportProtoDescriptorChanges(base, target IDLDocument) rpc.DescriptorCompatibilityReport {
	baseDescriptors := protoRuntimeDescriptorMap(base)
	targetDescriptors := protoRuntimeDescriptorMap(target)

	var report rpc.DescriptorCompatibilityReport
	for _, name := range sortedDescriptorKeys(baseDescriptors) {
		baseDescriptor := baseDescriptors[name]
		targetDescriptor, ok := targetDescriptors[name]
		if !ok {
			addRPCDescriptorChange(&report, rpc.DescriptorChangeService, rpc.DescriptorChangeBreaking,
				baseDescriptor.Name, "service was removed")
			mergeRPCDescriptorReport(&report, rpc.CompareDescriptors(baseDescriptor, rpc.Descriptor{Name: baseDescriptor.Name}))
			continue
		}
		mergeRPCDescriptorReport(&report, rpc.CompareDescriptors(baseDescriptor, targetDescriptor))
	}
	for _, name := range sortedDescriptorKeys(targetDescriptors) {
		if _, ok := baseDescriptors[name]; !ok {
			targetDescriptor := targetDescriptors[name]
			addRPCDescriptorChange(&report, rpc.DescriptorChangeService, rpc.DescriptorChangeInfo,
				targetDescriptor.Name, "service was added")
			mergeRPCDescriptorReport(&report, rpc.CompareDescriptors(rpc.Descriptor{Name: targetDescriptor.Name}, targetDescriptor))
		}
	}
	mergeRPCDescriptorReport(&report, protoWireDescriptorChanges(base, target))
	return report
}

func protoWireDescriptorChanges(base, target IDLDocument) rpc.DescriptorCompatibilityReport {
	var report rpc.DescriptorCompatibilityReport
	baseTypes := typeMap(base.Messages)
	targetTypes := typeMap(target.Messages)
	for _, name := range sortedIDLMessageKeys(baseTypes) {
		baseMessage := baseTypes[name]
		targetMessage, ok := targetTypes[name]
		if !ok {
			addRPCDescriptorChange(&report, rpc.DescriptorChangeType, rpc.DescriptorChangeBreaking,
				"message "+name, "message was removed")
			continue
		}
		addProtoFieldDescriptorChanges(&report, name, baseMessage.Fields, targetMessage.Fields)
	}
	for _, name := range sortedIDLMessageKeys(targetTypes) {
		if _, ok := baseTypes[name]; !ok {
			addRPCDescriptorChange(&report, rpc.DescriptorChangeType, rpc.DescriptorChangeInfo,
				"message "+name, "message was added")
		}
	}

	baseEnums := enumMap(base.Enums)
	targetEnums := enumMap(target.Enums)
	for _, name := range sortedIDLEnumKeys(baseEnums) {
		baseEnum := baseEnums[name]
		targetEnum, ok := targetEnums[name]
		if !ok {
			addRPCDescriptorChange(&report, rpc.DescriptorChangeEnum, rpc.DescriptorChangeBreaking,
				"enum "+name, "enum was removed")
			continue
		}
		addProtoEnumDescriptorChanges(&report, name, baseEnum.Values, targetEnum.Values)
	}
	for _, name := range sortedIDLEnumKeys(targetEnums) {
		if _, ok := baseEnums[name]; !ok {
			addRPCDescriptorChange(&report, rpc.DescriptorChangeEnum, rpc.DescriptorChangeInfo,
				"enum "+name, "enum was added")
		}
	}
	return report
}

func addProtoFieldDescriptorChanges(report *rpc.DescriptorCompatibilityReport, messageName string, base, target []IDLField) {
	baseByName := make(map[string]IDLField, len(base))
	baseByNumber := make(map[int]IDLField, len(base))
	for _, field := range base {
		baseByName[field.Name] = field
		if field.Number > 0 {
			baseByNumber[field.Number] = field
		}
	}
	targetByName := make(map[string]IDLField, len(target))
	targetByNumber := make(map[int]IDLField, len(target))
	for _, field := range target {
		targetByName[field.Name] = field
		if field.Number > 0 {
			targetByNumber[field.Number] = field
		}
	}

	for _, name := range sortedIDLFieldKeys(baseByName) {
		baseField := baseByName[name]
		targetField, ok := targetByName[name]
		if !ok {
			addRPCDescriptorChange(report, rpc.DescriptorChangeField, rpc.DescriptorChangeBreaking,
				fmt.Sprintf("%s.%s", messageName, name), "field was removed")
			continue
		}
		if baseField.Type != targetField.Type {
			addRPCDescriptorChange(report, rpc.DescriptorChangeField, rpc.DescriptorChangeBreaking,
				fmt.Sprintf("%s.%s", messageName, name),
				fmt.Sprintf("field type changed from %s to %s", baseField.Type, targetField.Type))
		}
		if baseField.Number > 0 && targetField.Number > 0 && baseField.Number != targetField.Number {
			addRPCDescriptorChange(report, rpc.DescriptorChangeField, rpc.DescriptorChangeBreaking,
				fmt.Sprintf("%s.%s", messageName, name),
				fmt.Sprintf("field number changed from %d to %d", baseField.Number, targetField.Number))
		}
	}
	for _, name := range sortedIDLFieldKeys(targetByName) {
		if _, ok := baseByName[name]; !ok {
			field := targetByName[name]
			addRPCDescriptorChange(report, rpc.DescriptorChangeField, rpc.DescriptorChangeInfo,
				fmt.Sprintf("%s.%s", messageName, name), fmt.Sprintf("field was added with number %d", field.Number))
		}
	}
	for _, number := range sortedIDLFieldNumbers(baseByNumber) {
		baseField := baseByNumber[number]
		targetField, ok := targetByNumber[number]
		if !ok || baseField.Name == targetField.Name {
			continue
		}
		addRPCDescriptorChange(report, rpc.DescriptorChangeField, rpc.DescriptorChangeBreaking,
			fmt.Sprintf("%s.%d", messageName, number),
			fmt.Sprintf("field number reused from %s to %s", baseField.Name, targetField.Name))
	}
}

func addProtoEnumDescriptorChanges(report *rpc.DescriptorCompatibilityReport, enumName string, base, target []IDLEnumValue) {
	baseByName := make(map[string]IDLEnumValue, len(base))
	baseByNumber := make(map[int]IDLEnumValue, len(base))
	for _, value := range base {
		baseByName[value.Name] = value
		baseByNumber[value.Number] = value
	}
	targetByName := make(map[string]IDLEnumValue, len(target))
	targetByNumber := make(map[int]IDLEnumValue, len(target))
	for _, value := range target {
		targetByName[value.Name] = value
		targetByNumber[value.Number] = value
	}
	for _, name := range sortedIDLEnumValueKeys(baseByName) {
		baseValue := baseByName[name]
		targetValue, ok := targetByName[name]
		if !ok {
			addRPCDescriptorChange(report, rpc.DescriptorChangeEnum, rpc.DescriptorChangeBreaking,
				fmt.Sprintf("%s.%s", enumName, name), "enum value was removed")
			continue
		}
		if baseValue.Number != targetValue.Number {
			addRPCDescriptorChange(report, rpc.DescriptorChangeEnum, rpc.DescriptorChangeBreaking,
				fmt.Sprintf("%s.%s", enumName, name),
				fmt.Sprintf("enum value number changed from %d to %d", baseValue.Number, targetValue.Number))
		}
	}
	for _, name := range sortedIDLEnumValueKeys(targetByName) {
		if _, ok := baseByName[name]; !ok {
			value := targetByName[name]
			addRPCDescriptorChange(report, rpc.DescriptorChangeEnum, rpc.DescriptorChangeInfo,
				fmt.Sprintf("%s.%s", enumName, name), fmt.Sprintf("enum value was added with number %d", value.Number))
		}
	}
	for _, number := range sortedIDLEnumValueNumbers(baseByNumber) {
		baseValue := baseByNumber[number]
		targetValue, ok := targetByNumber[number]
		if !ok || baseValue.Name == targetValue.Name {
			continue
		}
		addRPCDescriptorChange(report, rpc.DescriptorChangeEnum, rpc.DescriptorChangeBreaking,
			fmt.Sprintf("%s.%d", enumName, number),
			fmt.Sprintf("enum value number reused from %s to %s", baseValue.Name, targetValue.Name))
	}
}

func protoRuntimeDescriptorMap(doc IDLDocument) map[string]rpc.Descriptor {
	out := make(map[string]rpc.Descriptor, len(doc.Services))
	for _, service := range doc.Services {
		name := exportName(service.Name)
		descriptor := rpc.Descriptor{
			Name:    serviceFullName(doc, service.Name),
			Methods: make([]rpc.MethodDescriptor, 0, len(service.Methods)),
			Streams: make([]rpc.StreamDescriptor, 0, len(service.Methods)),
		}
		for _, method := range service.Methods {
			if method.ClientStream || method.ServerStream {
				descriptor.Streams = append(descriptor.Streams, rpc.StreamDescriptor{
					Name:    exportName(method.Name),
					Message: protoStreamMessage(method),
					Mode:    protoStreamMode(method),
					Metadata: map[string]string{
						"request":      exportName(method.Request),
						"response":     exportName(method.Response),
						"clientStream": boolString(method.ClientStream),
						"serverStream": boolString(method.ServerStream),
					},
				})
				continue
			}
			descriptor.Methods = append(descriptor.Methods, rpc.MethodDescriptor{
				Name:     exportName(method.Name),
				Request:  exportName(method.Request),
				Response: exportName(method.Response),
			})
		}
		sort.Slice(descriptor.Methods, func(i, j int) bool { return descriptor.Methods[i].Name < descriptor.Methods[j].Name })
		sort.Slice(descriptor.Streams, func(i, j int) bool { return descriptor.Streams[i].Name < descriptor.Streams[j].Name })
		out[name] = descriptor
	}
	return out
}

func protoStreamMessage(method IDLMethod) string {
	if method.ClientStream && method.ServerStream {
		return exportName(method.Request)
	}
	if method.ClientStream {
		return exportName(method.Request)
	}
	return exportName(method.Response)
}

func protoStreamMode(method IDLMethod) rpc.StreamMode {
	switch {
	case method.ClientStream && method.ServerStream:
		return rpc.StreamModeBidiStream
	case method.ClientStream:
		return rpc.StreamModeClientStream
	case method.ServerStream:
		return rpc.StreamModeServerStream
	default:
		return rpc.StreamModeUnary
	}
}

func sortedDescriptorKeys(descriptors map[string]rpc.Descriptor) []string {
	names := make([]string, 0, len(descriptors))
	for name := range descriptors {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func mergeRPCDescriptorReport(dst *rpc.DescriptorCompatibilityReport, src rpc.DescriptorCompatibilityReport) {
	for _, change := range src.Changes {
		addRPCDescriptorChange(dst, change.Category, change.Severity, change.Subject, change.Description)
	}
}

func addRPCDescriptorChange(report *rpc.DescriptorCompatibilityReport, category rpc.DescriptorChangeCategory, severity rpc.DescriptorChangeSeverity, subject, description string) {
	report.Changes = append(report.Changes, rpc.DescriptorChange{
		Category:    category,
		Severity:    severity,
		Subject:     subject,
		Description: description,
	})
	if severity == rpc.DescriptorChangeBreaking {
		report.Breaking++
	} else if severity == rpc.DescriptorChangeWarning {
		report.Warnings++
	}
}

func descriptorCompatibilityToBreakingReport(report rpc.DescriptorCompatibilityReport) BreakingChangesReport {
	var out BreakingChangesReport
	for _, change := range report.Changes {
		out.add(descriptorCategoryToChangeCategory(change.Category), descriptorSeverityToChangeSeverity(change.Severity), change.Subject, change.Description)
	}
	return out
}

func descriptorCategoryToChangeCategory(category rpc.DescriptorChangeCategory) ChangeCategory {
	switch category {
	case rpc.DescriptorChangeService:
		return CategoryService
	case rpc.DescriptorChangeMethod:
		return CategoryMethod
	case rpc.DescriptorChangeStream:
		return CategoryStream
	case rpc.DescriptorChangeType:
		return CategoryType
	case rpc.DescriptorChangeField:
		return CategoryField
	case rpc.DescriptorChangeEnum:
		return CategoryEnum
	case rpc.DescriptorChangeSignature:
		return CategorySignature
	case rpc.DescriptorChangeVersion:
		return CategoryService
	case rpc.DescriptorChangeTimeout:
		return CategorySignature
	default:
		return CategoryService
	}
}

func descriptorSeverityToChangeSeverity(severity rpc.DescriptorChangeSeverity) ChangeSeverity {
	switch severity {
	case rpc.DescriptorChangeBreaking:
		return SeverityBreaking
	case rpc.DescriptorChangeWarning:
		return SeverityWarning
	default:
		return SeverityInfo
	}
}

// reportIDLChanges 实现比对逻辑，同时适用于 .api 与 .proto（因为二者共享 IDLDocument）。
func reportIDLChanges(base, target IDLDocument) BreakingChangesReport {
	var r BreakingChangesReport

	if base.Package != "" && target.Package != "" && base.Package != target.Package {
		r.add(CategoryPackage, SeverityBreaking,
			fmt.Sprintf("package %q → %q", base.Package, target.Package),
			"package 命名变更会破坏所有下游 import 路径")
	}

	// 类型：删除 / 字段新增 / 字段删除 / 字段类型变更
	baseTypes := typeMap(base.Messages)
	targetTypes := typeMap(target.Messages)
	for name, b := range baseTypes {
		t, ok := targetTypes[name]
		if !ok {
			r.add(CategoryType, SeverityBreaking,
				"type "+name, "删除了类型，下游反序列化会失败")
			continue
		}
		r.compareFields(name, b.Fields, t.Fields)
	}

	// 枚举：删除值或改变 number（proto 语义）
	baseEnums := enumMap(base.Enums)
	targetEnums := enumMap(target.Enums)
	for name, b := range baseEnums {
		t, ok := targetEnums[name]
		if !ok {
			r.add(CategoryEnum, SeverityBreaking, "enum "+name, "删除了枚举")
			continue
		}
		r.compareEnumValues(name, b.Values, t.Values)
	}

	// 服务 / 方法：删除 / 变更签名
	baseServices := serviceMap(base.Services)
	targetServices := serviceMap(target.Services)

	// 服务级删除
	for name, b := range baseServices {
		_, ok := targetServices[name]
		if !ok {
			r.add(CategoryService, SeverityBreaking,
				"service "+name, fmt.Sprintf("删除了服务，包含 %d 个方法", len(b.Methods)))
		}
	}

	// 方法级删除/变更
	for name, b := range baseServices {
		t, ok := targetServices[name]
		if !ok {
			continue
		}
		baseMethods := methodMap(b.Methods)
		targetMethods := methodMap(t.Methods)
		for mname, bm := range baseMethods {
			tm, ok := targetMethods[mname]
			if !ok {
				r.add(CategoryMethod, SeverityBreaking,
					fmt.Sprintf("%s.%s", name, mname),
					"删除了方法，下游调用会失败")
				continue
			}
			if bm.Request != tm.Request {
				r.add(CategorySignature, SeverityBreaking,
					fmt.Sprintf("%s.%s request", name, mname),
					fmt.Sprintf("请求类型从 %s 变更为 %s", bm.Request, tm.Request))
			}
			if bm.Response != tm.Response {
				r.add(CategorySignature, SeverityBreaking,
					fmt.Sprintf("%s.%s response", name, mname),
					fmt.Sprintf("响应类型从 %s 变更为 %s", bm.Response, tm.Response))
			}
			if bm.ClientStream != tm.ClientStream || bm.ServerStream != tm.ServerStream {
				r.add(CategorySignature, SeverityBreaking,
					fmt.Sprintf("%s.%s streaming", name, mname),
					fmt.Sprintf("流式签名变更 client=%v/%v server=%v/%v",
						bm.ClientStream, tm.ClientStream, bm.ServerStream, tm.ServerStream))
			}
			// 对 .api 语义：HTTP 路径/方法变更
			if bm.HTTPMethod != "" || bm.HTTPPath != "" {
				if bm.HTTPMethod != tm.HTTPMethod || bm.HTTPPath != tm.HTTPPath {
					r.add(CategoryRoute, SeverityBreaking,
						fmt.Sprintf("%s %s → %s %s", bm.HTTPMethod, bm.HTTPPath, tm.HTTPMethod, tm.HTTPPath),
						"HTTP 路由签名变更")
				}
			}
		}
	}

	// 方法 / 路由新增（信息性；不破坏老客户端）
	// 类型 / 服务新增（信息性）
	for name := range targetTypes {
		if _, ok := baseTypes[name]; !ok {
			r.add(CategoryType, SeverityInfo, "type "+name, "新增类型")
		}
	}
	for name, t := range targetServices {
		if _, ok := baseServices[name]; !ok {
			r.add(CategoryService, SeverityInfo, "service "+name,
				fmt.Sprintf("新增服务，包含 %d 个方法", len(t.Methods)))
			continue
		}
		b := baseServices[name]
		bm := methodMap(b.Methods)
		for mname := range methodMap(t.Methods) {
			if _, ok := bm[mname]; !ok {
				r.add(CategoryMethod, SeverityInfo,
					fmt.Sprintf("%s.%s", name, mname), "新增方法")
			}
		}
	}

	return r
}

func (r *BreakingChangesReport) add(cat ChangeCategory, sev ChangeSeverity, subject, desc string) {
	r.Changes = append(r.Changes, BreakingChange{Category: cat, Severity: sev, Subject: subject, Description: desc})
	if sev == SeverityBreaking {
		r.Breaking++
	} else if sev == SeverityWarning {
		r.Warnings++
	}
}

func (r *BreakingChangesReport) compareFields(typeName string, base, target []IDLField) {
	bm := make(map[string]IDLField, len(base))
	for _, f := range base {
		bm[f.Name] = f
	}
	tm := make(map[string]IDLField, len(target))
	for _, f := range target {
		tm[f.Name] = f
	}

	// 字段删除 → 可能破坏下游（proto 默认不破坏，但 gofly .api 字段通常参与绑定）
	for name := range bm {
		if _, ok := tm[name]; !ok {
			r.add(CategoryField, SeverityBreaking,
				fmt.Sprintf("%s.%s", typeName, name), "删除了字段")
		}
	}
	// 字段类型变更
	for name, bf := range bm {
		tf, ok := tm[name]
		if !ok {
			continue
		}
		if bf.Type != tf.Type {
			r.add(CategoryField, SeverityBreaking,
				fmt.Sprintf("%s.%s", typeName, name),
				fmt.Sprintf("字段类型从 %s 变更为 %s", bf.Type, tf.Type))
		}
		// proto 的 field number 变更也被视为破坏
		if bf.Number > 0 && tf.Number > 0 && bf.Number != tf.Number {
			r.add(CategoryField, SeverityBreaking,
				fmt.Sprintf("%s.%s", typeName, name),
				fmt.Sprintf("字段 number 从 %d 变更为 %d", bf.Number, tf.Number))
		}
	}
	// 字段新增 → 告警级别（信息性）
	for name := range tm {
		if _, ok := bm[name]; !ok {
			r.add(CategoryField, SeverityInfo, fmt.Sprintf("%s.%s", typeName, name), "新增字段")
		}
	}
}

func (r *BreakingChangesReport) compareEnumValues(enumName string, base, target []IDLEnumValue) {
	bm := make(map[string]int, len(base))
	for _, v := range base {
		bm[v.Name] = v.Number
	}
	tm := make(map[string]int, len(target))
	for _, v := range target {
		tm[v.Name] = v.Number
	}

	for name, bn := range bm {
		tn, ok := tm[name]
		if !ok {
			r.add(CategoryEnum, SeverityBreaking,
				fmt.Sprintf("%s.%s", enumName, name), "删除了枚举值")
			continue
		}
		if bn != tn {
			r.add(CategoryEnum, SeverityBreaking,
				fmt.Sprintf("%s.%s", enumName, name),
				fmt.Sprintf("枚举值 number 从 %d 变更为 %d", bn, tn))
		}
	}
	for name := range tm {
		if _, ok := bm[name]; !ok {
			r.add(CategoryEnum, SeverityInfo, fmt.Sprintf("%s.%s", enumName, name), "新增枚举值")
		}
	}
}

// FormatBreakingText 将报告格式化为人类可读文本。
func FormatBreakingText(r BreakingChangesReport) []byte {
	var b strings.Builder
	if r.IsEmpty() {
		b.WriteString("No breaking changes\n")
		return []byte(b.String())
	}

	// 按 severity → category 排序，方便阅读
	groups := map[ChangeSeverity]map[ChangeCategory][]BreakingChange{}
	for _, c := range r.Changes {
		if groups[c.Severity] == nil {
			groups[c.Severity] = map[ChangeCategory][]BreakingChange{}
		}
		groups[c.Severity][c.Category] = append(groups[c.Severity][c.Category], c)
	}

	order := []ChangeSeverity{SeverityBreaking, SeverityWarning, SeverityInfo}
	categoryOrder := []ChangeCategory{
		CategoryPackage, CategoryService, CategoryMethod, CategoryRoute,
		CategoryStream, CategorySignature, CategoryType, CategoryField, CategoryEnum,
	}
	for _, sev := range order {
		perCat, ok := groups[sev]
		if !ok {
			continue
		}
		for _, cat := range categoryOrder {
			items, ok := perCat[cat]
			if !ok || len(items) == 0 {
				continue
			}
			sort.SliceStable(items, func(i, j int) bool { return items[i].Subject < items[j].Subject })
			fmt.Fprintf(&b, "[%s] %s (%d)\n", strings.ToUpper(string(sev)), cat, len(items))
			for _, c := range items {
				fmt.Fprintf(&b, "  - %s: %s\n", c.Subject, c.Description)
			}
		}
	}
	fmt.Fprintf(&b, "\nsummary: %d breaking, %d warnings, %d total changes\n",
		r.Breaking, r.Warnings, len(r.Changes))
	return []byte(b.String())
}

// ---- 工具方法 -----------------------------------------------------------

func typeMap(messages []IDLMessage) map[string]IDLMessage {
	out := make(map[string]IDLMessage, len(messages))
	for _, m := range messages {
		out[exportName(m.Name)] = m
	}
	return out
}

func enumMap(enums []IDLEnum) map[string]IDLEnum {
	out := make(map[string]IDLEnum, len(enums))
	for _, e := range enums {
		out[exportName(e.Name)] = e
	}
	return out
}

func serviceMap(services []IDLService) map[string]IDLService {
	out := make(map[string]IDLService, len(services))
	for _, s := range services {
		out[exportName(s.Name)] = s
	}
	return out
}

func methodMap(methods []IDLMethod) map[string]IDLMethod {
	out := make(map[string]IDLMethod, len(methods))
	for _, m := range methods {
		out[exportName(m.Name)] = m
	}
	return out
}

func sortedIDLMessageKeys(messages map[string]IDLMessage) []string {
	names := make([]string, 0, len(messages))
	for name := range messages {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedIDLEnumKeys(enums map[string]IDLEnum) []string {
	names := make([]string, 0, len(enums))
	for name := range enums {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedIDLFieldKeys(fields map[string]IDLField) []string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedIDLFieldNumbers(fields map[int]IDLField) []int {
	numbers := make([]int, 0, len(fields))
	for number := range fields {
		numbers = append(numbers, number)
	}
	sort.Ints(numbers)
	return numbers
}

func sortedIDLEnumValueKeys(values map[string]IDLEnumValue) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedIDLEnumValueNumbers(values map[int]IDLEnumValue) []int {
	numbers := make([]int, 0, len(values))
	for number := range values {
		numbers = append(numbers, number)
	}
	sort.Ints(numbers)
	return numbers
}
