package main

import (
	"fmt"
	"strings"
)

// emitGoModels writes the server-side DTOs. The backend generates its wire types from
// the same contract as the clients so the three can't drift apart.
func emitGoModels(s *Spec) string {
	var b strings.Builder
	fmt.Fprintf(&b, banner, "backend/openapi.yaml")
	b.WriteString("//\n// Wire types for the Pod API. Hand-editing this file is how a contract starts\n// lying; change backend/openapi.yaml and run `make generate` instead.\n\npackage api\n\n")

	body := strings.Builder{}
	needsTime := false
	for _, sc := range s.Schemas {
		switch {
		case len(sc.Enum) > 0:
			body.WriteString(goEnum(sc))
		case sc.Type == "object" || len(sc.Props) > 0:
			str, t := goStruct(s, sc)
			body.WriteString(str)
			needsTime = needsTime || t
		default:
			body.WriteString(goAlias(sc))
		}
		body.WriteString("\n")
	}
	if needsTime {
		b.WriteString("import \"time\"\n\n")
	}
	b.WriteString(strings.TrimRight(body.String(), "\n") + "\n")
	return b.String()
}

func goEnum(sc *Schema) string {
	var b strings.Builder
	b.WriteString(docComment("", "// ", sc.Description))
	fmt.Fprintf(&b, "type %s string\n\nconst (\n", sc.Name)
	for _, v := range sc.Enum {
		fmt.Fprintf(&b, "\t%s%s %s = %q\n", sc.Name, goPascal(v), sc.Name, v)
	}
	b.WriteString(")\n\n")
	fmt.Fprintf(&b, "// Known%ss lists every value this build understands. A peer may send others.\nfunc Known%ss() []%s {\n\treturn []%s{", sc.Name, sc.Name, sc.Name, sc.Name)
	for i, v := range sc.Enum {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s%s", sc.Name, goPascal(v))
	}
	b.WriteString("}\n}\n\n")
	fmt.Fprintf(&b, "// IsKnown reports whether v is a value this build understands.\nfunc (v %s) IsKnown() bool {\n\tfor _, known := range Known%ss() {\n\t\tif v == known {\n\t\t\treturn true\n\t\t}\n\t}\n\treturn false\n}\n", sc.Name, sc.Name)
	return b.String()
}

func goAlias(sc *Schema) string {
	var b strings.Builder
	b.WriteString(docComment("", "// ", sc.Description))
	fmt.Fprintf(&b, "type %s %s\n", sc.Name, goScalar(sc.Type, sc.Format))
	return b.String()
}

func goStruct(s *Spec, sc *Schema) (string, bool) {
	var b strings.Builder
	usesTime := false
	b.WriteString(docComment("", "// ", sc.Description))
	fmt.Fprintf(&b, "type %s struct {\n", sc.Name)
	for i, p := range sc.Props {
		if i > 0 && p.Description != "" {
			b.WriteString("\n")
		}
		b.WriteString(docComment("\t", "// ", p.Description))
		typ, t := goType(s, p.Type, p.Required)
		usesTime = usesTime || t
		tag := p.Name
		if !p.Required {
			tag += ",omitempty"
		}
		fmt.Fprintf(&b, "\t%s %s `json:%q`\n", goPascal(p.Name), typ, tag)
	}
	b.WriteString("}\n")
	return b.String(), usesTime
}

func goType(s *Spec, t *TypeRef, required bool) (string, bool) {
	base, usesTime := goBaseType(s, t)
	pointer := !required
	switch {
	case t.Ref != "":
		if target := s.schemaByName[t.Ref]; target != nil && len(target.Enum) > 0 {
			pointer = false // the zero value "" is already "absent" for a string enum
		}
	case t.Kind == "array" || t.Kind == "map":
		pointer = false // nil is already "absent"
	}
	if pointer {
		return "*" + base, usesTime
	}
	return base, usesTime
}

func goBaseType(s *Spec, t *TypeRef) (string, bool) {
	if t.Ref != "" {
		return t.Ref, false
	}
	switch t.Kind {
	case "array":
		elem, ut := goBaseType(s, t.Elem)
		return "[]" + elem, ut
	case "map":
		elem, ut := goBaseType(s, t.Elem)
		return "map[string]" + elem, ut
	default:
		typ := goScalar(t.Kind, t.Format)
		return typ, typ == "time.Time"
	}
}

func goScalar(kind, format string) string {
	switch kind {
	case "string":
		if format == "date-time" {
			return "time.Time"
		}
		return "string"
	case "integer":
		return "int"
	case "number":
		return "float64"
	case "boolean":
		return "bool"
	default:
		return "string"
	}
}
