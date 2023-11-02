package main

import (
	"io"
	"os"

	"github.com/golang/protobuf/proto"
	"github.com/yrbb/protoc-gen-rain/generator"
)

func main() {
	if len(os.Args) == 4 && os.Args[1] == "genhandler" {
		generator.GenHandler(os.Args[2], os.Args[3])
		return
	}

	g := generator.New()

	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		g.Error(err, "reading input")
	}

	if err := proto.Unmarshal(data, g.Request); err != nil {
		g.Error(err, "parsing input proto")
	}

	if len(g.Request.FileToGenerate) == 0 {
		g.Fail("no files to generate")
	}

	g.CommandLineParameters(g.Request.GetParameter())
	g.WrapTypes()
	g.SetPackageNames()
	g.BuildTypeNameMap()
	g.GenerateAllFiles()

	data, err = proto.Marshal(g.Response)
	if err != nil {
		g.Error(err, "failed to marshal output proto")
	}

	_, err = os.Stdout.Write(data)
	if err != nil {
		g.Error(err, "failed to write output proto")
	}
}
