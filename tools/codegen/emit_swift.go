package main

import (
	"fmt"
	"strings"
)

func emitSwiftModels(s *Spec) string {
	var b strings.Builder
	fmt.Fprintf(&b, banner, "backend/openapi.yaml")
	b.WriteString(`//
// Wire models for the Pod API.
//
// Nothing here is hand-written and nothing here should be: change
// backend/openapi.yaml and run ` + "`make generate`" + `.

import Foundation

`)
	for _, sc := range s.Schemas {
		switch {
		case len(sc.Enum) > 0:
			b.WriteString(swiftEnum(sc))
		case sc.Type == "object" || len(sc.Props) > 0:
			b.WriteString(swiftStruct(s, sc))
		default:
			b.WriteString(docComment("", "/// ", sc.Description))
			fmt.Fprintf(&b, "public typealias %s = %s\n", sc.Name, swiftScalar(sc.Type, sc.Format))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// swiftEnum emits an open enum: a raw-value struct rather than a Swift `enum`, so a
// value added to the contract by a newer server does not crash an older client.
func swiftEnum(sc *Schema) string {
	var b strings.Builder
	b.WriteString(docComment("", "/// ", sc.Description))
	b.WriteString("///\n/// Open enum: an unrecognised value from a newer server decodes rather than throwing.\n")
	fmt.Fprintf(&b, "public struct %s: RawRepresentable, Codable, Hashable, Sendable {\n", sc.Name)
	b.WriteString("    public let rawValue: String\n\n    public init(rawValue: String) {\n        self.rawValue = rawValue\n    }\n\n")
	for _, v := range sc.Enum {
		fmt.Fprintf(&b, "    public static let %s = %s(rawValue: %q)\n", swiftIdent(camel(v)), sc.Name, v)
	}
	b.WriteString("\n    /// Every value this build knows about, in contract order.\n")
	fmt.Fprintf(&b, "    public static let knownCases: [%s] = [", sc.Name)
	for i, v := range sc.Enum {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("." + swiftIdent(camel(v)))
	}
	b.WriteString("]\n\n    public var isKnown: Bool { Self.knownCases.contains(self) }\n}\n")
	return b.String()
}

func swiftStruct(s *Spec, sc *Schema) string {
	var b strings.Builder
	b.WriteString(docComment("", "/// ", sc.Description))
	fmt.Fprintf(&b, "public struct %s: Codable, Hashable, Sendable {\n", sc.Name)
	for i, p := range sc.Props {
		if i > 0 && p.Description != "" {
			b.WriteString("\n")
		}
		b.WriteString(docComment("    ", "/// ", p.Description))
		fmt.Fprintf(&b, "    public let %s: %s\n", swiftIdent(camel(p.Name)), swiftType(s, p.Type, p.Required))
	}

	// Memberwise initialiser — the synthesised one is internal, and previews and tests
	// live outside this module.
	b.WriteString("\n    public init(\n")
	for i, p := range sc.Props {
		comma := ","
		if i == len(sc.Props)-1 {
			comma = ""
		}
		def := ""
		if !p.Required {
			def = " = nil"
		}
		fmt.Fprintf(&b, "        %s: %s%s%s\n", swiftIdent(camel(p.Name)), swiftType(s, p.Type, p.Required), def, comma)
	}
	b.WriteString("    ) {\n")
	for _, p := range sc.Props {
		id := swiftIdent(camel(p.Name))
		fmt.Fprintf(&b, "        self.%s = %s\n", id, id)
	}
	b.WriteString("    }\n")

	if needsSwiftCodingKeys(sc) {
		b.WriteString("\n    private enum CodingKeys: String, CodingKey {\n")
		for _, p := range sc.Props {
			id := swiftIdent(camel(p.Name))
			if id == p.Name {
				fmt.Fprintf(&b, "        case %s\n", id)
			} else {
				fmt.Fprintf(&b, "        case %s = %q\n", id, p.Name)
			}
		}
		b.WriteString("    }\n")
	}
	b.WriteString("}\n")
	return b.String()
}

func needsSwiftCodingKeys(sc *Schema) bool {
	for _, p := range sc.Props {
		if swiftIdent(camel(p.Name)) != p.Name {
			return true
		}
	}
	return false
}

func swiftType(s *Spec, t *TypeRef, required bool) string {
	base := swiftBaseType(s, t)
	if !required {
		return base + "?"
	}
	return base
}

func swiftBaseType(s *Spec, t *TypeRef) string {
	if t.Ref != "" {
		return t.Ref
	}
	switch t.Kind {
	case "array":
		return "[" + swiftBaseType(s, t.Elem) + "]"
	case "map":
		return "[String: " + swiftBaseType(s, t.Elem) + "]"
	default:
		return swiftScalar(t.Kind, t.Format)
	}
}

func swiftScalar(kind, format string) string {
	switch kind {
	case "string":
		switch format {
		case "date-time":
			return "Date"
		case "uuid":
			return "UUID"
		case "uri":
			return "URL"
		default:
			return "String"
		}
	case "integer":
		return "Int"
	case "number":
		return "Double"
	case "boolean":
		return "Bool"
	default:
		return "String"
	}
}

var swiftKeywords = map[string]bool{
	"default": true, "self": true, "public": true, "internal": true, "private": true,
	"protocol": true, "extension": true, "class": true, "struct": true, "enum": true,
	"return": true, "for": true, "in": true, "let": true, "var": true, "case": true,
	"where": true, "as": true, "is": true, "try": true, "catch": true, "throw": true,
	"init": true, "deinit": true, "operator": true, "repeat": true, "guard": true,
	"defer": true, "import": true, "func": true, "if": true, "else": true, "while": true,
	"switch": true, "break": true, "continue": true, "true": true, "false": true, "nil": true,
	"any": true, "some": true, "Type": true, "Protocol": true,
}

func swiftIdent(s string) string {
	if swiftKeywords[s] {
		return "`" + s + "`"
	}
	return s
}
