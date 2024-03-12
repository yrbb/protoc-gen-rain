package generator

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/protoc-gen-go/descriptor"
	plugin "github.com/golang/protobuf/protoc-gen-go/plugin"
	"google.golang.org/genproto/googleapis/api/annotations"
)

var regAnnotation = regexp.MustCompile(`\s?\@tag\s+(.+)`)

// A GoImportPath is the import path of a Go package. e.g., "google.golang.org/genproto/protobuf".
type GoImportPath string

func (p GoImportPath) String() string { return strconv.Quote(string(p)) }

// A GoPackageName is the name of a Go package. e.g., "protobuf".
type GoPackageName string

// Object is an interface abstracting the abilities shared by enums, messages, extensions and imported objects.
type Object interface {
	GoImportPath() GoImportPath
	TypeName() []string
	File() *FileDescriptor
}

// Generator is the type whose methods generate the output, stored in the associated response structure.
type Generator struct {
	*bytes.Buffer

	Request  *plugin.CodeGeneratorRequest  // The input.
	Response *plugin.CodeGeneratorResponse // The output.

	Param             map[string]string // Command-line parameters.
	PackageImportPath string            // Go import path of the package we're generating code for
	ImportPrefix      string            // String to prefix to imported package file names.
	ImportMap         map[string]string // Mapping from .proto file name to import path

	Pkg map[string]string // The names under which we import support packages

	outputImportPath GoImportPath                   // Package we're generating code for.
	allFiles         []*FileDescriptor              // All files in the tree
	allFilesByName   map[string]*FileDescriptor     // All files by filename.
	genFiles         []*FileDescriptor              // Those files we will generate output for.
	file             *FileDescriptor                // The file we are compiling now.
	packageNames     map[GoImportPath]GoPackageName // Imported package names in the current file.
	usedPackages     map[GoImportPath]bool          // Packages used in current file.
	usedPackageNames map[GoPackageName]bool         // Package names used in the current file.
	addedImports     map[GoImportPath]bool          // Additional imports to emit.
	typeNameToObject map[string]Object              // Key is a fully-qualified name in input syntax.
	init             []string                       // Lines to emit in the init function.
	indent           string
	pathType         pathType // How to generate output filenames.
	writeOutput      bool
}

type pathType int

const (
	pathTypeImport pathType = iota
	pathTypeSourceRelative
)

// New creates a new generator and allocates the request and response protobufs.
func New() *Generator {
	g := new(Generator)
	g.Buffer = new(bytes.Buffer)
	g.Request = new(plugin.CodeGeneratorRequest)
	g.Response = new(plugin.CodeGeneratorResponse)
	return g
}

// Error reports a problem, including an error, and exits the program.
func (g *Generator) Error(err error, msgs ...string) {
	s := strings.Join(msgs, " ") + ":" + err.Error()
	log.Print("protoc-gen-rain: error:", s)
	os.Exit(1)
}

// Fail reports a problem and exits the program.
func (g *Generator) Fail(msgs ...string) {
	s := strings.Join(msgs, " ")
	log.Print("protoc-gen-rain: error:", s)
	os.Exit(1)
}

// CommandLineParameters breaks the comma-separated list of key=value pairs
// in the parameter (a member of the request protobuf) into a key/value map.
// It then sets file name mappings defined by those entries.
func (g *Generator) CommandLineParameters(parameter string) {
	g.Param = make(map[string]string)
	for _, p := range strings.Split(parameter, ",") {
		if i := strings.Index(p, "="); i < 0 {
			g.Param[p] = ""
		} else {
			g.Param[p[0:i]] = p[i+1:]
		}
	}

	g.ImportMap = make(map[string]string)
	for k, v := range g.Param {
		switch k {
		case "import_prefix":
			g.ImportPrefix = v
		case "import_path":
			g.PackageImportPath = v
		case "paths":
			switch v {
			case "import":
				g.pathType = pathTypeImport
			case "source_relative":
				g.pathType = pathTypeSourceRelative
			default:
				g.Fail(fmt.Sprintf(`Unknown path type %q: want "import" or "source_relative".`, v))
			}
		default:
			if len(k) > 0 && k[0] == 'M' {
				g.ImportMap[k[1:]] = v
			}
		}
	}

	if g.ImportPrefix == "" {
		g.ImportPrefix = g.Param["repo"] + "/"
	}
}

// DefaultPackageName returns the package name printed for the object.
// If its file is in a different package, it returns the package name we're using for this file, plus ".".
// Otherwise it returns the empty string.
func (g *Generator) DefaultPackageName(obj Object) string {
	importPath := obj.GoImportPath()
	importPath = GoImportPath(obj.File().GetName())

	if importPath == g.outputImportPath {
		return ""
	}

	return string(g.GoPackageName(importPath)) + "."
}

// GoPackageName returns the name used for a package.
func (g *Generator) GoPackageName(importPath GoImportPath) GoPackageName {
	if name, ok := g.packageNames[importPath]; ok {
		return name
	}

	name := cleanPackageName(baseName(string(importPath)))
	for i, orig := 1, name; g.usedPackageNames[name] || isGoPredeclaredIdentifier[string(name)]; i++ {
		name = orig + GoPackageName(strconv.Itoa(i))
	}

	g.packageNames[importPath] = name
	g.usedPackageNames[name] = true

	return name
}

// AddImport adds a package to the generated file's import section.
// It returns the name used for the package.
func (g *Generator) AddImport(importPath GoImportPath) GoPackageName {
	g.addedImports[importPath] = true
	return g.GoPackageName(importPath)
}

var globalPackageNames = map[GoPackageName]bool{}

var isGoPredeclaredIdentifier = map[string]bool{
	"append":     true,
	"bool":       true,
	"byte":       true,
	"cap":        true,
	"close":      true,
	"complex":    true,
	"complex128": true,
	"complex64":  true,
	"copy":       true,
	"delete":     true,
	"error":      true,
	"false":      true,
	"float32":    true,
	"float64":    true,
	"imag":       true,
	"int":        true,
	"int16":      true,
	"int32":      true,
	"int64":      true,
	"int8":       true,
	"iota":       true,
	"len":        true,
	"make":       true,
	"new":        true,
	"nil":        true,
	"panic":      true,
	"print":      true,
	"println":    true,
	"real":       true,
	"recover":    true,
	"rune":       true,
	"string":     true,
	"true":       true,
	"uint":       true,
	"uint16":     true,
	"uint32":     true,
	"uint64":     true,
	"uint8":      true,
	"uintptr":    true,
}

// defaultGoPackage returns the package name to use,
// derived from the import path of the package we're building code for.
func (g *Generator) defaultGoPackage() GoPackageName {
	p := g.PackageImportPath
	if i := strings.LastIndex(p, "/"); i >= 0 {
		p = p[i+1:]
	}
	return cleanPackageName(p)
}

// SetPackageNames sets the package name for this run.
// The package name must agree across all files being generated.
// It also defines unique package names for all imported files.
func (g *Generator) SetPackageNames() {
	g.outputImportPath = g.genFiles[0].importPath
	g.outputImportPath = GoImportPath(g.genFiles[0].GetName())

	defaultPackageNames := make(map[GoImportPath]GoPackageName)
	for _, f := range g.genFiles {
		if _, p, ok := f.goPackageOption(); ok {
			defaultPackageNames[f.importPath] = p
		}
	}
	for _, f := range g.genFiles {
		if _, p, ok := f.goPackageOption(); ok {
			// Source file: option go_package = "quux/bar";
			f.packageName = p
		} else if p, ok := defaultPackageNames[f.importPath]; ok {
			// A go_package option in another file in the same package.
			//
			// This is a poor choice in general, since every source file should
			// contain a go_package option. Supported mainly for historical
			// compatibility.
			f.packageName = p
		} else if p := g.defaultGoPackage(); p != "" {
			// Command-line: import_path=quux/bar.
			//
			// The import_path flag sets a package name for files which don't
			// contain a go_package option.
			f.packageName = p
		} else if p := f.GetPackage(); p != "" {
			// Source file: package quux.bar;
			f.packageName = cleanPackageName(p)
		} else {
			// Source filename.
			f.packageName = cleanPackageName(baseName(f.GetName()))
		}
	}

	// Check that all files have a consistent package name and import path.
	for _, f := range g.genFiles[1:] {
		if a, b := g.genFiles[0].importPath, f.importPath; a != b {
			g.Fail(fmt.Sprintf("inconsistent package import paths: %v, %v", a, b))
		}
		if a, b := g.genFiles[0].packageName, f.packageName; a != b {
			g.Fail(fmt.Sprintf("inconsistent package names: %v, %v", a, b))
		}
	}

	// Names of support packages. These never vary (if there are conflicts,
	// we rename the conflicting package), so this could be removed someday.
	g.Pkg = map[string]string{
		"fmt":   "fmt",
		"math":  "math",
		"proto": "proto",
	}
}

// WrapTypes walks the incoming data, wrapping DescriptorProtos, EnumDescriptorProtos
// and FileDescriptorProtos into file-referenced objects within the Generator.
// It also creates the list of files to generate and so should be called before GenerateAllFiles.
func (g *Generator) WrapTypes() {
	g.allFiles = make([]*FileDescriptor, 0, len(g.Request.ProtoFile))
	g.allFilesByName = make(map[string]*FileDescriptor, len(g.allFiles))
	genFileNames := make(map[string]bool)
	for _, n := range g.Request.FileToGenerate {
		genFileNames[n] = true
	}
	for _, f := range g.Request.ProtoFile {
		fd := &FileDescriptor{
			FileDescriptorProto: f,
			exported:            make(map[Object][]symbol),
			proto3:              fileIsProto3(f),
		}
		// The import path may be set in a number of ways.
		if substitution, ok := g.ImportMap[f.GetName()]; ok {
			// Command-line: M=foo.proto=quux/bar.
			//
			// Explicit mapping of source file to import path.
			fd.importPath = GoImportPath(substitution)
		} else if genFileNames[f.GetName()] && g.PackageImportPath != "" {
			// Command-line: import_path=quux/bar.
			//
			// The import_path flag sets the import path for every file that
			// we generate code for.
			fd.importPath = GoImportPath(g.PackageImportPath)
		} else if p, _, _ := fd.goPackageOption(); p != "" {
			// Source file: option go_package = "quux/bar";
			//
			// The go_package option sets the import path. Most users should use this.
			fd.importPath = p
		} else {
			// Source filename.
			//
			// Last resort when nothing else is available.
			fd.importPath = GoImportPath(path.Dir(f.GetName()))
		}
		// We must wrap the descriptors before we wrap the enums
		fd.desc = wrapDescriptors(fd)
		g.buildNestedDescriptors(fd.desc)
		fd.enum = wrapEnumDescriptors(fd, fd.desc)
		g.buildNestedEnums(fd.desc, fd.enum)
		fd.ext = wrapExtensions(fd)
		extractComments(fd)
		g.allFiles = append(g.allFiles, fd)
		g.allFilesByName[f.GetName()] = fd
	}
	for _, fd := range g.allFiles {
		fd.imp = wrapImported(fd, g)
	}

	g.genFiles = make([]*FileDescriptor, 0, len(g.Request.FileToGenerate))
	for _, fileName := range g.Request.FileToGenerate {
		fd := g.allFilesByName[fileName]
		if fd == nil {
			g.Fail("could not find file named", fileName)
		}
		g.genFiles = append(g.genFiles, fd)
	}
}

// Scan the descriptors in this file.  For each one, build the slice of nested descriptors
func (g *Generator) buildNestedDescriptors(descs []*Descriptor) {
	for _, desc := range descs {
		if len(desc.NestedType) != 0 {
			for _, nest := range descs {
				if nest.parent == desc {
					desc.nested = append(desc.nested, nest)
				}
			}
			if len(desc.nested) != len(desc.NestedType) {
				g.Fail("internal error: nesting failure for", desc.GetName())
			}
		}
	}
}

func (g *Generator) buildNestedEnums(descs []*Descriptor, enums []*EnumDescriptor) {
	for _, desc := range descs {
		if len(desc.EnumType) != 0 {
			for _, enum := range enums {
				if enum.parent == desc {
					desc.enums = append(desc.enums, enum)
				}
			}
			if len(desc.enums) != len(desc.EnumType) {
				g.Fail("internal error: enum nesting failure for", desc.GetName())
			}
		}
	}
}

// BuildTypeNameMap builds the map from fully qualified type names to objects.
// The key names for the map come from the input data, which puts a period at the beginning.
// It should be called after SetPackageNames and before GenerateAllFiles.
func (g *Generator) BuildTypeNameMap() {
	g.typeNameToObject = make(map[string]Object)
	for _, f := range g.allFiles {
		// The names in this loop are defined by the proto world, not us, so the
		// package name may be empty.  If so, the dotted package name of X will
		// be ".X"; otherwise it will be ".pkg.X".
		dottedPkg := "." + f.GetPackage()
		if dottedPkg != "." {
			dottedPkg += "."
		}
		for _, enum := range f.enum {
			name := dottedPkg + dottedSlice(enum.TypeName())
			g.typeNameToObject[name] = enum
		}
		for _, desc := range f.desc {
			name := dottedPkg + dottedSlice(desc.TypeName())
			g.typeNameToObject[name] = desc
		}
	}
}

// ObjectNamed, given a fully-qualified input type name as it appears in the input data,
// returns the descriptor for the message or enum with that name.
func (g *Generator) ObjectNamed(typeName string) Object {
	o, ok := g.typeNameToObject[typeName]
	if !ok {
		g.Fail("can't find object with type", typeName)
	}
	return o
}

// printAtom prints the (atomic, non-annotation) argument to the generated output.
func (g *Generator) printAtom(v interface{}) {
	switch v := v.(type) {
	case string:
		g.WriteString(v)
	case *string:
		g.WriteString(*v)
	case bool:
		fmt.Fprint(g, v)
	case *bool:
		fmt.Fprint(g, *v)
	case int:
		fmt.Fprint(g, v)
	case *int32:
		fmt.Fprint(g, *v)
	case *int64:
		fmt.Fprint(g, *v)
	case float64:
		fmt.Fprint(g, v)
	case *float64:
		fmt.Fprint(g, *v)
	case GoPackageName:
		g.WriteString(string(v))
	case GoImportPath:
		g.WriteString(strconv.Quote(string(v)))
	default:
		g.Fail(fmt.Sprintf("unknown type in printer: %T", v))
	}
}

// P prints the arguments to the generated output.  It handles strings and int32s, plus
// handling indirections because they may be *string, etc.  Any inputs of type AnnotatedAtoms may emit
// annotations in a .meta file in addition to outputting the atoms themselves (if g.annotateCode
// is true).
func (g *Generator) P(str ...interface{}) {
	if !g.writeOutput {
		return
	}
	g.WriteString(g.indent)
	for _, v := range str {
		switch v := v.(type) {
		case *AnnotatedAtoms:
			for _, v := range v.atoms {
				g.printAtom(v)
			}
		default:
			g.printAtom(v)
		}
	}
	g.WriteByte('\n')
}

// addInitf stores the given statement to be printed inside the file's init function.
// The statement is given as a format specifier and arguments.
func (g *Generator) addInitf(stmt string, a ...interface{}) {
	g.init = append(g.init, fmt.Sprintf(stmt, a...))
}

// In Indents the output one tab stop.
func (g *Generator) In() { g.indent += "\t" }

// Out unindents the output one tab stop.
func (g *Generator) Out() {
	if len(g.indent) > 0 {
		g.indent = g.indent[1:]
	}
}

// GenerateAllFiles generates the output for all the files we're outputting.
func (g *Generator) GenerateAllFiles() {
	// Generate the output. The generator runs for every file, even the files
	// that we don't generate output for, so that we can collate the full list
	// of exported symbols to support public imports.
	genFileMap := make(map[*FileDescriptor]bool, len(g.genFiles))
	for _, file := range g.genFiles {
		genFileMap[file] = true
	}

	for _, file := range g.allFiles {
		// model file
		g.Reset()
		g.writeOutput = genFileMap[file]
		g.generateModelFile(file)
		if !g.writeOutput {
			continue
		}
		fname := file.goFileName(g.pathType, "model")
		g.Response.File = append(g.Response.File, &plugin.CodeGeneratorResponse_File{
			Name:    proto.String(fname),
			Content: proto.String(g.String()),
		})

		// api file
		g.Reset()
		g.writeOutput = genFileMap[file]
		g.generateApiFile(file)
		if !g.writeOutput {
			continue
		}
		fname = file.goFileName(g.pathType, "api")
		g.Response.File = append(g.Response.File, &plugin.CodeGeneratorResponse_File{
			Name:    proto.String(fname),
			Content: proto.String(g.String()),
		})
	}
}

// Fill the response protocol buffer with the generated output for all the files we're
// supposed to generate.
func (g *Generator) generateApiFile(file *FileDescriptor) {
	g.file = file
	g.usedPackages = make(map[GoImportPath]bool)
	g.packageNames = make(map[GoImportPath]GoPackageName)
	g.usedPackageNames = make(map[GoPackageName]bool)
	g.addedImports = make(map[GoImportPath]bool)
	for name := range globalPackageNames {
		g.usedPackageNames[name] = true
	}

	g.P()

	for _, td := range g.file.imp {
		g.generateImported(td)
	}

	hasBinding := false
	if len(file.FileDescriptorProto.Service) > 0 {
		for i, service := range file.FileDescriptorProto.Service {
			binding := g.generateService(file, service, i)
			if !hasBinding && binding {
				hasBinding = true
			}
		}
	}

	rem := g.Buffer
	g.Buffer = new(bytes.Buffer)
	g.generateHeader()

	if len(file.FileDescriptorProto.Service) > 0 {
		g.generateImports("api", hasBinding)
	}

	if !g.writeOutput {
		return
	}
	g.Write(rem.Bytes())

	// Reformat generated code and patch annotation locations.
	fset := token.NewFileSet()
	original := g.Bytes()
	fileAST, err := parser.ParseFile(fset, "", original, parser.ParseComments)
	if err != nil {
		// Print out the bad code with line numbers.
		// This should never happen in practice, but it can while changing generated code,
		// so consider this a debugging aid.
		var src bytes.Buffer
		s := bufio.NewScanner(bytes.NewReader(original))
		for line := 1; s.Scan(); line++ {
			fmt.Fprintf(&src, "%5d\t%s\n", line, s.Bytes())
		}
		g.Fail("bad Go source code was generated:", err.Error(), "\n"+src.String())
	}
	ast.SortImports(fset, fileAST)
	g.Reset()
	err = (&printer.Config{Mode: printer.TabIndent | printer.UseSpaces, Tabwidth: 8}).Fprint(g, fset, fileAST)
	if err != nil {
		g.Fail("generated Go source code could not be reformatted:", err.Error())
	}
}

func (g *Generator) generateHandler(k, v string) {
	p := g.Param["path"] + "/handler.json"
	bts, err := os.ReadFile(p)
	if err != nil {
		g.Fail("handler.json file not found")
	}

	m := map[string]string{}
	if err := json.Unmarshal(bts, &m); err != nil {
		g.Fail("handler.json file content error")
	}

	m[k] = v

	bts, _ = json.Marshal(m)
	os.WriteFile(p, bts, 0o777)
}

func (g *Generator) generateService(file *FileDescriptor, service *descriptor.ServiceDescriptorProto, index int) bool {
	path := fmt.Sprintf("6,%d", index)

	origServName := service.GetName()
	serviceName := strings.ToLower(service.GetName())
	if pkg := file.GetPackage(); pkg != "" {
		serviceName = pkg
	}
	servName := CamelCase(origServName)

	g.P()
	g.P()

	// Client interface.
	g.P("type ", servName, "Handler interface {")
	for _, method := range service.Method {
		g.P(g.generateClientSignature(serviceName, servName, method))
	}
	g.P("}")
	g.P()

	g.P(`func Register` + servName + `Handler(g *gin.Engine, h ` + servName + `Handler) {`)

	hasBinding := false
	for i, method := range service.Method {
		customAnnotations := map[string]string{}
		if cs, ok := g.makeComments(fmt.Sprintf("%s,2,%d", path, i)); ok {
			if g.writeOutput {
				g.P(cs)
			}

			if res := regAnnotation.FindStringSubmatch(cs); len(res) > 1 {
				for _, h := range strings.Split(res[1], " ") {
					key, val := strings.Trim(h, " "), ""
					if strings.Contains(key, ":") {
						arr := strings.Split(key, ":")
						key, val = arr[0], arr[1]
					}

					customAnnotations[key] = val
				}
			}
		}

		binding := g.generateClientMethod(serviceName, servName, method, customAnnotations)
		if !hasBinding && binding {
			hasBinding = true
		}
	}

	g.P("}")
	g.P()

	fname := file.goFileName(g.pathType, "api")
	fpath := filepath.Dir(fname)
	g.generateHandler(fpath+"/"+servName, fpath)

	return hasBinding
}

var reservedClientName = map[string]bool{}

func (g *Generator) typeName(str string) string {
	g.RecordTypeUse(str)
	return g.TypeName(g.ObjectNamed(str))
}

func (g *Generator) generateClientSignature(reqServ, servName string, method *descriptor.MethodDescriptorProto) string {
	origMethName := method.GetName()
	methName := CamelCase(origMethName)
	if reservedClientName[methName] {
		methName += "_"
	}

	g.RecordTypeUse(method.GetInputType())

	in := g.typeName(method.GetInputType())

	if in == "types.Empty" || in == "empty.Empty" {
		in = "router.Empty"
	}

	input := ", in *" + in
	outName := g.typeName(method.GetOutputType())
	output := ", out *" + outName

	return fmt.Sprintf("%s(ctx *gin.Context%s%s) error", methName, input, output)
}

func (g *Generator) generateClientMethod(reqServ, servName string, method *descriptor.MethodDescriptorProto, customAnnotations map[string]string) bool {
	gec := os.Getenv("GEN_ERROR_CODE")
	if gec == "" {
		gec = "500"
	}

	origMethName := method.GetName()
	methName := CamelCase(origMethName)
	if reservedClientName[methName] {
		methName += "_"
	}

	needBind := true

	inType := g.typeName(method.GetInputType())
	if inType == "types.Empty" || inType == "empty.Empty" {
		inType = "router.Empty"
		needBind = false
	} else {
		for _, desc := range g.file.desc {
			if desc.GetOptions().GetMapEntry() {
				continue
			}

			if goTypeName := CamelCaseSlice(desc.TypeName()); goTypeName == inType {
				if len(desc.Field) == 0 {
					needBind = false
				}

				break
			}
		}
	}

	outType := g.typeName(method.GetOutputType())
	if strings.HasPrefix(outType, reqServ+".") {
		outType = strings.TrimPrefix(outType, reqServ+".")
	}

	isGet := false
	noJSON := false

	middlewares := []string{}
	if val, ok := customAnnotations["middleware"]; ok {
		middlewares = strings.Split(val, ",")
	}

	bindCheck := true
	if val, ok := customAnnotations["bindcheck"]; ok && strings.EqualFold(val, "false") {
		bindCheck = false
	}

	binding := "json"
	if val, ok := customAnnotations["binding"]; ok {
		binding = val
	}

	if method.Options != nil && proto.HasExtension(method.Options, annotations.E_Http) {
		ext, _ := proto.GetExtension(method.Options, annotations.E_Http)
		if opts, ok := ext.(*annotations.HttpRule); ok {
			if getapi, ok := opts.Pattern.(*annotations.HttpRule_Get); ok {
				isGet = true
				url := getapi.Get

				if len(middlewares) > 0 {
					g.P(`router.Handle(g, "GET", "` + url + `", []string{"` + strings.Join(middlewares, `","`) + `"}, func(ctx *gin.Context) {`)
				} else {
					g.P(`g.GET("` + url + `", func(ctx *gin.Context) {`)
				}
			}

			if postapi, ok := opts.Pattern.(*annotations.HttpRule_Post); ok {
				url := postapi.Post

				if len(middlewares) > 0 {
					g.P(`router.Handle(g, "POST", "` + url + `", []string{"` + strings.Join(middlewares, `","`) + `"}, func(ctx *gin.Context) {`)
				} else {
					g.P(`g.POST("` + url + `", func(ctx *gin.Context) {`)
				}
			}

			if opts.ResponseBody != "" && opts.ResponseBody != "json" {
				noJSON = true
			}
		}
	} else {
		g.Fail("option google.api.http not found")
	}

	if needBind {
		bindingMth := ""
		bindingType := ""
		switch strings.ToLower(binding) {
		case "form":
			bindingMth = "ShouldBindWith"
			bindingType = "Form"
		case "query":
			bindingMth = "ShouldBindWith"
			bindingType = "Query"
		case "formpost":
			bindingMth = "ShouldBindWith"
			bindingType = "FormPost"
		case "formmultipart":
			bindingMth = "ShouldBindWith"
			bindingType = "FormMultipart"
		default:
			bindingMth = "ShouldBindBodyWith"
			bindingType = "JSON"
		}

		g.P(`input, output := ` + inType + "{}, " + outType + "{}")
		g.P()
		if !bindCheck {
			if isGet {
				g.P(`_ = ctx.ShouldBindQuery(&input)`)
			} else {
				g.P(`_ = ctx.` + bindingMth + `(&input, binding.` + bindingType + `)`)
			}
		} else {
			if isGet {
				g.P(`if err := ctx.ShouldBindQuery(&input); err != nil {`)
			} else {
				g.P(`if err := ctx.` + bindingMth + `(&input, binding.` + bindingType + `); err != nil {`)
			}
			g.P(`router.Error(ctx, ` + gec + `, err)`)
			g.P(`return`)
			g.P(`}`)
		}
		g.P()
	} else {
		g.P(`input := ` + inType + `{}`)
		g.P(`var output ` + outType)
		g.P()
	}

	if noJSON {
		g.P(`_ = h.` + methName + `(ctx, &input, &output)`)
	} else {
		g.P(`err := h.` + methName + `(ctx.Copy(), &input, &output)`)
		g.P(`if err != nil {`)
		g.P(`router.Error(ctx, ` + gec + `, err)`)
		g.P(`return`)
		g.P(`}`)
		g.P()
		g.P(`router.JSON(ctx, &output)`)
	}
	g.P("})")
	g.P()

	return needBind
}

// Fill the response protocol buffer with the generated output for all the files we're
// supposed to generateModelFile.
func (g *Generator) generateModelFile(file *FileDescriptor) {
	g.file = file
	g.usedPackages = make(map[GoImportPath]bool)
	g.packageNames = make(map[GoImportPath]GoPackageName)
	g.usedPackageNames = make(map[GoPackageName]bool)
	g.addedImports = make(map[GoImportPath]bool)
	for name := range globalPackageNames {
		g.usedPackageNames[name] = true
	}

	for _, td := range g.file.imp {
		g.generateImported(td)
	}

	for _, enum := range g.file.enum {
		g.generateEnum(enum)
	}

	serviceName := ""
	if pkg := file.GetPackage(); pkg != "" {
		serviceName = pkg
	}

	for _, desc := range g.file.desc {
		// Don't generate virtual messages for maps.
		if desc.GetOptions().GetMapEntry() {
			continue
		}

		g.generateMessage(desc, serviceName)
	}

	// Generate header and imports last, though they appear first in the output.
	rem := g.Buffer
	g.Buffer = new(bytes.Buffer)
	g.generateHeader()
	g.generateImports("model", false)
	if !g.writeOutput {
		return
	}
	g.Write(rem.Bytes())

	// Reformat generated code and patch annotation locations.
	fset := token.NewFileSet()
	original := g.Bytes()
	fileAST, err := parser.ParseFile(fset, "", original, parser.ParseComments)
	if err != nil {
		// Print out the bad code with line numbers.
		// This should never happen in practice, but it can while changing generated code,
		// so consider this a debugging aid.
		var src bytes.Buffer
		s := bufio.NewScanner(bytes.NewReader(original))
		for line := 1; s.Scan(); line++ {
			fmt.Fprintf(&src, "%5d\t%s\n", line, s.Bytes())
		}
		g.Fail("bad Go source code was generated:", err.Error(), "\n"+src.String())
	}
	ast.SortImports(fset, fileAST)
	g.Reset()
	err = (&printer.Config{Mode: printer.TabIndent | printer.UseSpaces, Tabwidth: 8}).Fprint(g, fset, fileAST)
	if err != nil {
		g.Fail("generated Go source code could not be reformatted:", err.Error())
	}
}

// Generate the header, including package definition
func (g *Generator) generateHeader() {
	g.P("// Code generated by protoc-gen-rain. DO NOT EDIT.")
	if g.file.GetOptions().GetDeprecated() {
		g.P("// ", g.file.Name, " is a deprecated file.")
	} else {
		g.P("// source: ", g.file.Name)
	}

	g.P()
	g.PrintComments(strconv.Itoa(packagePath))
	g.P()
	g.P("package ", strings.ToLower(string(g.file.packageName)))
	g.P()
}

// deprecationComment is the standard comment added to deprecated
// messages, fields, enums, and enum values.
var deprecationComment = "// Deprecated: Do not use."

// PrintComments prints any comments from the source .proto file.
// The path is a comma-separated list of integers.
// It returns an indication of whether any comments were printed.
// See descriptor.proto for its format.
func (g *Generator) PrintComments(path string) bool {
	if !g.writeOutput {
		return false
	}
	if c, ok := g.makeComments(path); ok {
		g.P(c)
		return true
	}
	return false
}

// makeComments generates the comment string for the field, no "\n" at the end
func (g *Generator) makeComments(path string) (string, bool) {
	loc, ok := g.file.comments[path]
	if !ok {
		return "", false
	}
	w := new(bytes.Buffer)
	nl := ""
	for _, line := range strings.Split(strings.TrimSuffix(loc.GetLeadingComments(), "\n"), "\n") {
		fmt.Fprintf(w, "%s//%s", nl, line)
		nl = "\n"
	}
	return w.String(), true
}

func (g *Generator) fileByName(filename string) *FileDescriptor {
	return g.allFilesByName[filename]
}

// weak returns whether the ith import of the current file is a weak import.
func (g *Generator) weak(i int32) bool {
	for _, j := range g.file.WeakDependency {
		if j == i {
			return true
		}
	}
	return false
}

// Generate the imports
func (g *Generator) generateImports(typ string, hasBinding bool) {
	imports := make(map[GoPackageName]GoPackageName)
	for i, s := range g.file.Dependency {
		// Do not import weak imports.
		if g.weak(int32(i)) {
			continue
		}

		importPath := GoImportPath(s)

		if strings.Contains(string(importPath), "/protobuf/") ||
			strings.Contains(string(importPath), "google/api") ||
			strings.Contains(string(importPath), "/googleapis/") {
			continue
		}

		packageName := g.GoPackageName(importPath)
		if _, ok := g.usedPackages[importPath]; !ok {
			continue
		}

		if _, ok := imports[packageName]; ok {
			continue
		}

		imports[packageName] = packageName
	}

	// for importPath := range g.addedImports {
	// 	imports[importPath] = g.GoPackageName(importPath)
	// }

	// We almost always need a proto import.  Rather than computing when we
	// do, which is tricky when there's a plugin, just import it and
	// reference it later. The same argument applies to the fmt and math packages.

	if typ == "model" {
		g.generateModelImports(imports)
	} else {
		g.generateApiImports(imports, hasBinding)
	}
}

func (g *Generator) generateModelImports(imports map[GoPackageName]GoPackageName) {
	if len(imports) == 0 {
		return
	}

	g.P("import (")
	for importPath := range imports {
		g.P(`"` + g.ImportPrefix + string(importPath) + `"`)
	}
	g.P(")")
	g.P()
	g.P()
}

func (g *Generator) generateApiImports(imports map[GoPackageName]GoPackageName, hasBinding bool) {
	g.P("import (")
	g.P(`"github.com/gin-gonic/gin"`)
	if hasBinding {
		g.P(`"github.com/gin-gonic/gin/binding"`)
	}
	g.P()
	g.P(`"`, g.Param["repo"], `/router"`)
	for importPath := range imports {
		g.P(`"` + g.ImportPrefix + string(importPath) + `"`)
	}
	g.P(")")
	g.P()
	g.P()
}

func (g *Generator) generateImported(id *ImportedDescriptor) {
	df := id.o.File()
	filename := *df.Name

	if df.importPath == g.file.importPath && *df.Package == *g.file.Package {
		return
	}

	g.usedPackages[df.importPath] = true

	for _, sym := range df.exported[id.o] {
		sym.GenerateAlias(g, filename, g.GoPackageName(df.importPath))
	}

	g.P()
}

// Generate the enum definitions for this EnumDescriptor.
func (g *Generator) generateEnum(enum *EnumDescriptor) {
	// The full type name
	typeName := enum.TypeName()
	// The full type name, CamelCased.
	ccTypeName := CamelCaseSlice(typeName)
	ccPrefix := enum.prefix()

	deprecatedEnum := ""
	if enum.GetOptions().GetDeprecated() {
		deprecatedEnum = deprecationComment
	}
	g.PrintComments(enum.path)
	g.P("type ", Annotate(enum.file, enum.path, ccTypeName), " int32", deprecatedEnum)
	g.file.addExport(enum, enumSymbol{ccTypeName, enum.proto3()})
	g.P("const (")
	for i, e := range enum.Value {
		etorPath := fmt.Sprintf("%s,%d,%d", enum.path, enumValuePath, i)
		g.PrintComments(etorPath)

		deprecatedValue := ""
		if e.GetOptions().GetDeprecated() {
			deprecatedValue = deprecationComment
		}

		name := ccPrefix + *e.Name
		g.P(Annotate(enum.file, etorPath, name), " ", ccTypeName, " = ", e.Number, " ", deprecatedValue)
		g.file.addExport(enum, constOrVarSymbol{name, "const", ccTypeName})
	}
	g.P(")")
	g.P()
	g.generateEnumRegistration(enum)
}

// TypeName is the printed name appropriate for an item. If the object is in the current file,
// TypeName drops the package name and underscores the rest.
// Otherwise the object is from another package; and the result is the underscored
// package name followed by the item name.
// The result always has an initial capital.
func (g *Generator) TypeName(obj Object) string {
	return g.DefaultPackageName(obj) + CamelCaseSlice(obj.TypeName())
}

// GoType returns a string representing the type name, and the wire type
func (g *Generator) GoType(serviceName string, message *Descriptor, field *descriptor.FieldDescriptorProto) (typ string, wire string) {
	// TODO: Options.
	switch *field.Type {
	case descriptor.FieldDescriptorProto_TYPE_DOUBLE:
		typ, wire = "float64", "fixed64"
	case descriptor.FieldDescriptorProto_TYPE_FLOAT:
		typ, wire = "float32", "fixed32"
	case descriptor.FieldDescriptorProto_TYPE_INT64:
		typ, wire = "int64", "varint"
	case descriptor.FieldDescriptorProto_TYPE_UINT64:
		typ, wire = "uint64", "varint"
	case descriptor.FieldDescriptorProto_TYPE_INT32:
		typ, wire = "int32", "varint"
	case descriptor.FieldDescriptorProto_TYPE_UINT32:
		typ, wire = "uint32", "varint"
	case descriptor.FieldDescriptorProto_TYPE_FIXED64:
		typ, wire = "uint64", "fixed64"
	case descriptor.FieldDescriptorProto_TYPE_FIXED32:
		typ, wire = "uint32", "fixed32"
	case descriptor.FieldDescriptorProto_TYPE_BOOL:
		typ, wire = "bool", "varint"
	case descriptor.FieldDescriptorProto_TYPE_STRING:
		typ, wire = "string", "bytes"
	case descriptor.FieldDescriptorProto_TYPE_GROUP:
		desc := g.ObjectNamed(field.GetTypeName())
		typ, wire = "*"+g.TypeName(desc), "group"
	case descriptor.FieldDescriptorProto_TYPE_MESSAGE:
		desc := g.ObjectNamed(field.GetTypeName())

		typName := "*" + g.TypeName(desc)

		if typName == "*anypb.Any" || typName == "*any.Any" || typName == "*_struct.Value" || typName == "*struct.Values" {
			typName = "interface{}"
		}

		if typName == "*struct.Struct" || typName == "*_struct.Struct" {
			typName = "map[string]interface{}"
		}

		if typName == "*struct.ListValue" || typName == "*_struct.ListValue" {
			typName = "[]interface{}"
		}

		typ, wire = typName, "bytes"
	case descriptor.FieldDescriptorProto_TYPE_BYTES:
		typ, wire = "[]byte", "bytes"
	case descriptor.FieldDescriptorProto_TYPE_ENUM:
		desc := g.ObjectNamed(field.GetTypeName())
		typ, wire = g.TypeName(desc), "varint"
	case descriptor.FieldDescriptorProto_TYPE_SFIXED32:
		typ, wire = "int32", "fixed32"
	case descriptor.FieldDescriptorProto_TYPE_SFIXED64:
		typ, wire = "int64", "fixed64"
	case descriptor.FieldDescriptorProto_TYPE_SINT32:
		typ, wire = "int32", "zigzag32"
	case descriptor.FieldDescriptorProto_TYPE_SINT64:
		typ, wire = "int64", "zigzag64"
	default:
		g.Fail("unknown type for", field.GetName())
	}
	if isRepeated(field) {
		typ = "[]" + typ
	} else if message != nil && message.proto3() {
		return
	} else if field.OneofIndex != nil && message != nil {
		return
	} else if needsStar(*field.Type) {
		typ = "*" + typ
	}
	return
}

func (g *Generator) RecordTypeUse(t string) {
	if _, ok := g.typeNameToObject[t]; !ok {
		return
	}
	importFile := g.ObjectNamed(t).File().GetName()
	importPath := g.ObjectNamed(t).GoImportPath()
	importPath = GoImportPath(importFile)

	if importPath == g.outputImportPath {
		// Don't record use of objects in our package.
		return
	}

	g.AddImport(importPath)
	g.usedPackages[importPath] = true
}

// Method names that may be generated.  Fields with these names get an
// underscore appended. Any change to this set is a potential incompatible
// API change because it changes generated field names.
var methodNames = [...]string{
	"Reset",
	"String",
	"ProtoMessage",
	"Marshal",
	"Unmarshal",
	"ExtensionRangeArray",
	"ExtensionMap",
	"Descriptor",
}

// The different types of fields in a message and how to actually print them
// Most of the logic for generateMessage is in the methods of these types.
//
// Note that the content of the field is irrelevant, a simpleField can contain
// anything from a scalar to a group (which is just a message).
//
// Extension fields (and message sets) are however handled separately.
//
// simpleField - a field that is neiter weak nor oneof, possibly repeated
// oneofField - field containing list of subfields:
// - oneofSubField - a field within the oneof

// msgCtx contains the context for the generator functions.
type msgCtx struct {
	goName  string      // Go struct name of the message, e.g. MessageName
	message *Descriptor // The descriptor for the message
}

// generateMessageStruct adds the actual struct with it's members (but not methods) to the output.
func (g *Generator) generateMessageStruct(mc *msgCtx, topLevelFields []topLevelField) {
	comments := g.PrintComments(mc.message.path)

	// Guarantee deprecation comments appear after user-provided comments.
	if mc.message.GetOptions().GetDeprecated() {
		if comments {
			// Convention: Separate deprecation comments from original
			// comments with an empty line.
			g.P("//")
		}
		g.P(deprecationComment)
	}

	g.P("type ", Annotate(mc.message.file, mc.message.path, mc.goName), " struct {")
	for _, pf := range topLevelFields {
		pf.decl(g, mc)
	}
	g.P("}")
}

// Generate the type, methods and default constant definitions for this Descriptor.
func (g *Generator) generateMessage(message *Descriptor, serviceName string) {
	topLevelFields := []topLevelField{}
	// The full type name
	typeName := message.TypeName()
	// The full type name, CamelCased.
	goTypeName := CamelCaseSlice(typeName)

	usedNames := make(map[string]bool)
	for _, n := range methodNames {
		usedNames[n] = true
	}

	// allocNames finds a conflict-free variation of the given strings,
	// consistently mutating their suffixes.
	// It returns the same number of strings.
	allocNames := func(ns ...string) []string {
	Loop:
		for {
			for _, n := range ns {
				if usedNames[n] {
					for i := range ns {
						ns[i] += "_"
					}
					continue Loop
				}
			}
			for _, n := range ns {
				usedNames[n] = true
			}
			return ns
		}
	}

	mapFieldTypes := make(map[*descriptor.FieldDescriptorProto]string) // keep track of the map fields to be added later

	// Build a structure more suitable for generating the text in one pass
	for i, field := range message.Field {
		fieldFullPath := fmt.Sprintf("%s,%d,%d", message.path, messageFieldPath, i)
		commentStr, ok := g.makeComments(fieldFullPath)
		if ok {
			commentStr += "\n"
		}

		customAnnotations := map[string]string{}
		if res := regAnnotation.FindStringSubmatch(commentStr); len(res) > 1 {
			for _, h := range strings.Split(res[1], " ") {
				key, val := strings.Trim(h, " "), ""
				if strings.Contains(key, ":") {
					arr := strings.Split(key, ":")
					key, val = arr[0], arr[1]
				}

				customAnnotations[key] = val
			}
		}

		base := CamelCase(*field.Name)
		ns := allocNames(base, "Get"+base)
		fieldName, fieldGetterName := ns[0], ns[1]
		typename, _ := g.GoType(serviceName, message, field)

		jsonName := *field.Name
		if field.JsonName != nil {
			jsonName = *field.JsonName
		}

		formName := jsonName

		if val, ok := customAnnotations["omitempty"]; !ok || strings.EqualFold(val, "true") {
			jsonName += ",omitempty"
		}

		tag := fmt.Sprintf("json:%q form:%q", jsonName, formName)

		if *field.Type == descriptor.FieldDescriptorProto_TYPE_MESSAGE {
			desc := g.ObjectNamed(field.GetTypeName())
			if d, ok := desc.(*Descriptor); ok && d.GetOptions().GetMapEntry() {
				// Figure out the Go types and tags for the key and value types.
				keyField, valField := d.Field[0], d.Field[1]
				keyType, _ := g.GoType(serviceName, d, keyField)
				valType, _ := g.GoType(serviceName, d, valField)

				// We don't use stars, except for message-typed values.
				// Message and enum types are the only two possibly foreign types used in maps,
				// so record their use. They are not permitted as map keys.
				keyType = strings.TrimPrefix(keyType, "*")
				switch *valField.Type {
				case descriptor.FieldDescriptorProto_TYPE_ENUM:
					valType = strings.TrimPrefix(valType, "*")
					g.RecordTypeUse(valField.GetTypeName())
				case descriptor.FieldDescriptorProto_TYPE_MESSAGE:
					g.RecordTypeUse(valField.GetTypeName())
				default:
					valType = strings.TrimPrefix(valType, "*")
				}

				typename = fmt.Sprintf("map[%s]%s", keyType, valType)
				mapFieldTypes[field] = typename // record for the getter generation
			}
		}

		fieldDeprecated := ""
		if field.GetOptions().GetDeprecated() {
			fieldDeprecated = deprecationComment
		}

		rf := simpleField{
			fieldCommon: fieldCommon{
				goName:     fieldName,
				getterName: fieldGetterName,
				goType:     typename,
				tags:       tag,
				protoName:  field.GetName(),
				fullPath:   fieldFullPath,
			},
			protoTypeName: field.GetTypeName(),
			protoType:     *field.Type,
			deprecated:    fieldDeprecated,
			protoDef:      field.GetDefaultValue(),
			comment:       commentStr,
		}
		var pf topLevelField = &rf

		topLevelFields = append(topLevelFields, pf)
		g.RecordTypeUse(field.GetTypeName())
	}

	mc := &msgCtx{
		goName:  goTypeName,
		message: message,
	}

	g.generateMessageStruct(mc, topLevelFields)
	g.P()
}

func (g *Generator) generateEnumRegistration(enum *EnumDescriptor) {
	// // We always print the full (proto-world) package name here.
	pkg := enum.File().GetPackage()
	if pkg != "" {
		pkg += "."
	}
	// The full type name
	typeName := enum.TypeName()
	// The full type name, CamelCased.
	ccTypeName := CamelCaseSlice(typeName)
	g.addInitf("%s.RegisterEnum(%q, %[3]s_name, %[3]s_value)", g.Pkg["proto"], pkg+ccTypeName, ccTypeName)
}
