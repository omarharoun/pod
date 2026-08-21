package main

import (
	"fmt"
	"strings"
)

const kotlinPackage = "app.pod.api.generated"

func emitKotlinModels(s *Spec) string {
	var b strings.Builder
	fmt.Fprintf(&b, banner, "backend/openapi.yaml")
	b.WriteString("//\n// Wire models for the Pod API.\n//\n// Nothing here is hand-written and nothing here should be: change\n// backend/openapi.yaml and run `make generate`.\n\n")
	fmt.Fprintf(&b, "package %s\n\n", kotlinPackage)
	b.WriteString("import app.pod.api.WireEnum\nimport app.pod.api.WireEnumSerializer\nimport kotlinx.datetime.Instant\nimport kotlinx.serialization.SerialName\nimport kotlinx.serialization.Serializable\n\n")

	for _, sc := range s.Schemas {
		switch {
		case len(sc.Enum) > 0:
			b.WriteString(kotlinEnum(sc))
		case sc.Type == "object" || len(sc.Props) > 0:
			b.WriteString(kotlinDataClass(s, sc))
		default:
			b.WriteString(kdoc("", sc.Description))
			fmt.Fprintf(&b, "public typealias %s = %s\n", sc.Name, kotlinScalar(sc.Type, sc.Format))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// kotlinEnum emits a closed Kotlin enum plus an UNKNOWN sentinel, so a value added by a
// newer server deserialises instead of throwing on an older install.
func kotlinEnum(sc *Schema) string {
	var b strings.Builder
	b.WriteString(kdoc("", sc.Description, "An unrecognised value from a newer server decodes as [UNKNOWN] rather than throwing."))
	fmt.Fprintf(&b, "@Serializable(with = %sSerializer::class)\npublic enum class %s(override val wire: String) : WireEnum {\n", sc.Name, sc.Name)
	for _, v := range sc.Enum {
		fmt.Fprintf(&b, "    %s(%q),\n", screamingSnake(v), v)
	}
	b.WriteString("\n    /** A value this build does not know about. */\n    UNKNOWN(\"\"),\n    ;\n\n    public companion object {\n")
	fmt.Fprintf(&b, "        public fun fromWire(value: String): %s = entries.firstOrNull { it.wire == value } ?: UNKNOWN\n", sc.Name)
	b.WriteString("    }\n}\n\n")
	fmt.Fprintf(&b, "internal object %sSerializer : WireEnumSerializer<%s>(\n    %q,\n    { value -> %s.fromWire(value) },\n)\n", sc.Name, sc.Name, kotlinPackage+"."+sc.Name, sc.Name)
	return b.String()
}

func kotlinDataClass(s *Spec, sc *Schema) string {
	var b strings.Builder
	b.WriteString(kdoc("", sc.Description))
	b.WriteString("@Serializable\n")
	fmt.Fprintf(&b, "public data class %s(\n", sc.Name)
	for i, p := range sc.Props {
		if i > 0 && p.Description != "" {
			b.WriteString("\n")
		}
		b.WriteString(kdoc("    ", p.Description))
		name := kotlinIdent(camel(p.Name))
		if name != p.Name {
			fmt.Fprintf(&b, "    @SerialName(%q)\n", p.Name)
		}
		def := ""
		if !p.Required {
			def = " = null"
		}
		fmt.Fprintf(&b, "    val %s: %s%s,\n", name, kotlinType(s, p.Type, p.Required), def)
	}
	b.WriteString(")\n")
	return b.String()
}

func kdoc(indent, description string, extra ...string) string {
	parts := []string{}
	if strings.TrimSpace(description) != "" {
		parts = append(parts, strings.TrimSpace(description))
	}
	parts = append(parts, extra...)
	if len(parts) == 0 {
		return ""
	}
	body := strings.Join(parts, "\n\n")
	body = strings.ReplaceAll(body, "*/", "*\\/")
	var b strings.Builder
	b.WriteString(indent + "/**\n")
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == "" {
			b.WriteString(indent + " *\n")
			continue
		}
		b.WriteString(indent + " * " + strings.TrimRight(line, " ") + "\n")
	}
	b.WriteString(indent + " */\n")
	return b.String()
}

func kotlinType(s *Spec, t *TypeRef, required bool) string {
	base := kotlinBaseType(s, t)
	if !required {
		return base + "?"
	}
	return base
}

func kotlinBaseType(s *Spec, t *TypeRef) string {
	if t.Ref != "" {
		return t.Ref
	}
	switch t.Kind {
	case "array":
		return "List<" + kotlinBaseType(s, t.Elem) + ">"
	case "map":
		return "Map<String, " + kotlinBaseType(s, t.Elem) + ">"
	default:
		return kotlinScalar(t.Kind, t.Format)
	}
}

func kotlinScalar(kind, format string) string {
	switch kind {
	case "string":
		if format == "date-time" {
			return "Instant"
		}
		// uuid and uri stay String: Kotlin's UUID is still experimental and a URL type
		// buys nothing here. See docs/DECISIONS.md ADR-004.
		return "String"
	case "integer":
		return "Int"
	case "number":
		return "Double"
	case "boolean":
		return "Boolean"
	default:
		return "String"
	}
}

var kotlinKeywords = map[string]bool{
	"as": true, "break": true, "class": true, "continue": true, "do": true, "else": true,
	"false": true, "for": true, "fun": true, "if": true, "in": true, "interface": true,
	"is": true, "null": true, "object": true, "package": true, "return": true, "super": true,
	"this": true, "throw": true, "true": true, "try": true, "typealias": true, "typeof": true,
	"val": true, "var": true, "when": true, "while": true, "when_": true,
}

func kotlinIdent(s string) string {
	if kotlinKeywords[s] {
		return "`" + s + "`"
	}
	return s
}
