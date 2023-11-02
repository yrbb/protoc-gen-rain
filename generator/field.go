package generator

import (
	"github.com/golang/protobuf/protoc-gen-go/descriptor"
)

// fieldCommon contains data common to all types of fields.
type fieldCommon struct {
	goName     string // Go name of field, e.g. "FieldName" or "Descriptor_"
	protoName  string // Name of field in proto language, e.g. "field_name" or "descriptor"
	getterName string // Name of the getter, e.g. "GetFieldName" or "GetDescriptor_"
	goType     string // The Go type as a string, e.g. "*int32" or "*OtherMessage"
	tags       string // The tag string/annotation for the type, e.g. `protobuf:"varint,8,opt,name=region_id,json=regionId"`
	fullPath   string // The full path of the field as used by Annotate etc, e.g. "4,0,2,0"
}

// getProtoName gets the proto name of a field, e.g. "field_name" or "descriptor".
func (f *fieldCommon) getProtoName() string {
	return f.protoName
}

// getGoType returns the go type of the field  as a string, e.g. "*int32".
func (f *fieldCommon) getGoType() string {
	return f.goType
}

// simpleField is not weak, not a oneof, not an extension. Can be required, optional or repeated.
type simpleField struct {
	fieldCommon
	protoTypeName string                               // Proto type name, empty if primitive, e.g. ".google.protobuf.Duration"
	protoType     descriptor.FieldDescriptorProto_Type // Actual type enum value, e.g. descriptor.FieldDescriptorProto_TYPE_FIXED64
	deprecated    string                               // Deprecation comment, if any, e.g. "// Deprecated: Do not use."
	getterDef     string                               // Default for getters, e.g. "nil", `""` or "Default_MessageType_FieldName"
	protoDef      string                               // Default value as defined in the proto file, e.g "yoshi" or "5"
	comment       string                               // The full comment for the field, e.g. "// Useful information"
}

// decl prints the declaration of the field in the struct (if any).
func (f *simpleField) decl(g *Generator, mc *msgCtx) {
	g.P(f.comment, Annotate(mc.message.file, f.fullPath, f.goName), "\t", f.goType, "\t`", f.tags, "`", f.deprecated)
}

// getter prints the getter for the field.
func (f *simpleField) getter(g *Generator, mc *msgCtx) {}

// setter prints the setter method of the field.
func (f *simpleField) setter(g *Generator, mc *msgCtx) {}

// getProtoDef returns the default value explicitly stated in the proto file, e.g "yoshi" or "5".
func (f *simpleField) getProtoDef() string {
	return f.protoDef
}

// getProtoTypeName returns the protobuf type name for the field as returned by field.GetTypeName(), e.g. ".google.protobuf.Duration".
func (f *simpleField) getProtoTypeName() string {
	return f.protoTypeName
}

// getProtoType returns the *field.Type value, e.g. descriptor.FieldDescriptorProto_TYPE_FIXED64.
func (f *simpleField) getProtoType() descriptor.FieldDescriptorProto_Type {
	return f.protoType
}

// oneofSubFields are kept slize held by each oneofField. They do not appear in the top level slize of fields for the message.
type oneofSubField struct {
	fieldCommon
	protoTypeName string                               // Proto type name, empty if primitive, e.g. ".google.protobuf.Duration"
	protoType     descriptor.FieldDescriptorProto_Type // Actual type enum value, e.g. descriptor.FieldDescriptorProto_TYPE_FIXED64
	oneofTypeName string                               // Type name of the enclosing struct, e.g. "MessageName_FieldName"
	fieldNumber   int                                  // Actual field number, as defined in proto, e.g. 12
	getterDef     string                               // Default for getters, e.g. "nil", `""` or "Default_MessageType_FieldName"
	protoDef      string                               // Default value as defined in the proto file, e.g "yoshi" or "5"
	deprecated    string                               // Deprecation comment, if any.
}

// typedNil prints a nil casted to the pointer to this field.
// - for XXX_OneofWrappers
func (f *oneofSubField) typedNil(g *Generator) {
	g.P("(*", f.oneofTypeName, ")(nil),")
}

// getProtoDef returns the default value explicitly stated in the proto file, e.g "yoshi" or "5".
func (f *oneofSubField) getProtoDef() string {
	return f.protoDef
}

// getProtoTypeName returns the protobuf type name for the field as returned by field.GetTypeName(), e.g. ".google.protobuf.Duration".
func (f *oneofSubField) getProtoTypeName() string {
	return f.protoTypeName
}

// getProtoType returns the *field.Type value, e.g. descriptor.FieldDescriptorProto_TYPE_FIXED64.
func (f *oneofSubField) getProtoType() descriptor.FieldDescriptorProto_Type {
	return f.protoType
}

// oneofField represents the oneof on top level.
// The alternative fields within the oneof are represented by oneofSubField.
type oneofField struct {
	fieldCommon
	subFields []*oneofSubField // All the possible oneof fields
	comment   string           // The full comment for the field, e.g. "// Types that are valid to be assigned to MyOneof:\n\\"
}

// decl prints the declaration of the field in the struct (if any).
func (f *oneofField) decl(g *Generator, mc *msgCtx) {}

func (f *oneofField) getter(g *Generator, mc *msgCtx) {}

func (f *oneofField) setter(g *Generator, mc *msgCtx) {}

// topLevelField interface implemented by all types of fields on the top level (not oneofSubField).
type topLevelField interface {
	decl(g *Generator, mc *msgCtx)   // print declaration within the struct
	getter(g *Generator, mc *msgCtx) // print getter
	setter(g *Generator, mc *msgCtx) // print setter if applicable
}

// defField interface implemented by all types of fields that can have defaults (not oneofField, but instead oneofSubField).
type defField interface {
	getProtoDef() string                                // default value explicitly stated in the proto file, e.g "yoshi" or "5"
	getProtoName() string                               // proto name of a field, e.g. "field_name" or "descriptor"
	getGoType() string                                  // go type of the field  as a string, e.g. "*int32"
	getProtoTypeName() string                           // protobuf type name for the field, e.g. ".google.protobuf.Duration"
	getProtoType() descriptor.FieldDescriptorProto_Type // *field.Type value, e.g. descriptor.FieldDescriptorProto_TYPE_FIXED64
}
