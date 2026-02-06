// Generates JSON Schema from config structs using godoc comments.
// Run with: go run ./tools/schema > schema.json
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"reflect"

	"github.com/invopop/jsonschema"
	"github.com/sklarsa/incus-azure-pipelines/cmd"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

func main() {
	title := cases.Title(language.English)
	r := &jsonschema.Reflector{
		Namer: func(t reflect.Type) string {
			if t.PkgPath() == "" {
				return t.Name()
			}
			return title.String(path.Base(t.PkgPath())) + t.Name()
		},
	}

	// Extract descriptions from godoc comments in source files
	if err := r.AddGoComments("github.com/sklarsa/incus-azure-pipelines", "./"); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not load comments: %v\n", err)
	}

	schema := r.Reflect(&cmd.CLIConfig{})
	schema.ID = "https://github.com/sklarsa/incus-azure-pipelines/config-schema.json"
	schema.Title = "incus-azure-pipelines configuration"
	schema.Description = "Configuration file schema for incus-azure-pipelines daemon"

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error marshaling schema: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(string(data))
}
