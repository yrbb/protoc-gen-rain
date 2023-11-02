package generator

// AnnotatedAtoms is a list of atoms (as consumed by P) that records the file name and proto AST path from which they originated.
type AnnotatedAtoms struct {
	source string
	path   string
	atoms  []interface{}
}

// Annotate records the file name and proto AST path of a list of atoms
// so that a later call to P can emit a link from each atom to its origin.
func Annotate(file *FileDescriptor, path string, atoms ...interface{}) *AnnotatedAtoms {
	return &AnnotatedAtoms{source: *file.Name, path: path, atoms: atoms}
}
