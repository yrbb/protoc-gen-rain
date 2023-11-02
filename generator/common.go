package generator

import "github.com/golang/protobuf/protoc-gen-go/descriptor"

// The file and package name method are common to messages and enums.
type common struct {
	file *FileDescriptor // File this object comes from.
}

// GoImportPath is the import path of the Go package containing the type.
func (c *common) GoImportPath() GoImportPath {
	return c.file.importPath
}

func (c *common) File() *FileDescriptor { return c.file }

func fileIsProto3(file *descriptor.FileDescriptorProto) bool {
	return file.GetSyntax() == "proto3"
}

func (c *common) proto3() bool { return fileIsProto3(c.file.FileDescriptorProto) }
