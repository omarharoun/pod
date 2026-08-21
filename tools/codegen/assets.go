package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

func sortStrings(s []string) { sort.Strings(s) }

// orderedKeys returns the keys of a JSON object in the order the file lists them, so
// generated constants read the way the source file reads.
func orderedKeys(raw []byte) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("expected a JSON object")
	}
	var keys []string
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return nil, err
		}
		keys = append(keys, k.(string))
		var discard json.RawMessage
		if err := dec.Decode(&discard); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

func objectFields(raw json.RawMessage) ([]string, map[string]json.RawMessage, error) {
	keys, err := orderedKeys(raw)
	if err != nil {
		return nil, nil, err
	}
	var byKey map[string]json.RawMessage
	if err := json.Unmarshal(raw, &byKey); err != nil {
		return nil, nil, err
	}
	return keys, byKey, nil
}

// ------------------------------------------------------------------ microcopy

type CopyKey struct {
	Key          string
	Sample       string // the en-US text, for the doc comment only
	Plural       bool
	Placeholders []string
}

var pluralCategories = map[string]bool{"zero": true, "one": true, "two": true, "few": true, "many": true, "other": true}

// pluralFormsByKey is filled by loadCopyKeys so the Go catalogue can carry every CLDR
// form, not just the one used for documentation.
var pluralFormsByKey = map[string]map[string]string{}

func loadCopyKeys(path string) ([]CopyKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	keys, byKey, err := objectFields(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var out []CopyKey
	for _, k := range keys {
		if strings.HasPrefix(k, "$") {
			continue
		}
		entry, err := copyEntry(k, byKey[k])
		if err != nil {
			return nil, fmt.Errorf("%s: %s: %w", path, k, err)
		}
		out = append(out, entry)
	}
	return out, nil
}

func copyEntry(key string, raw json.RawMessage) (CopyKey, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return CopyKey{Key: key, Sample: s, Placeholders: placeholders(s)}, nil
	}
	forms, byForm, err := objectFields(raw)
	if err != nil {
		return CopyKey{}, fmt.Errorf("expected a string or a plural object")
	}
	entry := CopyKey{Key: key, Plural: true}
	collected := map[string]string{}
	for _, f := range forms {
		if !pluralCategories[f] {
			return CopyKey{}, fmt.Errorf("nested object with non-plural key %q — flatten it into a dotted key instead", f)
		}
		var text string
		if err := json.Unmarshal(byForm[f], &text); err != nil {
			return CopyKey{}, err
		}
		collected[f] = text
		if f == "other" || entry.Sample == "" {
			entry.Sample = text
			entry.Placeholders = placeholders(text)
		}
	}
	pluralFormsByKey[key] = collected
	return entry, nil
}

func placeholders(s string) []string {
	var out []string
	seen := map[string]bool{}
	for i := 0; i < len(s); i++ {
		if s[i] != '{' {
			continue
		}
		j := strings.IndexByte(s[i:], '}')
		if j < 0 {
			break
		}
		name := s[i+1 : i+j]
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
		i += j
	}
	return out
}

func copyDoc(k CopyKey) string {
	doc := fmt.Sprintf("%q", k.Sample)
	if k.Plural {
		doc += " (plural key — resolve with a count)"
	}
	if len(k.Placeholders) > 0 {
		doc += fmt.Sprintf("\nPlaceholders: %s", strings.Join(k.Placeholders, ", "))
	}
	return doc
}

func emitSwiftCopy(keys []CopyKey) string {
	var b strings.Builder
	fmt.Fprintf(&b, banner, "content/microcopy.json")
	b.WriteString(`//
// Every user-facing string in the app is looked up through one of these keys.
// A literal in a view is a bug: see CLAUDE.md §2 rule 1.

import Foundation

/// A key into ` + "`content/microcopy.json`" + `.
public struct CopyKey: RawRepresentable, Hashable, Sendable {
    public let rawValue: String

    public init(rawValue: String) {
        self.rawValue = rawValue
    }
}

public extension CopyKey {
`)
	for _, k := range keys {
		b.WriteString(docComment("    ", "/// ", copyDoc(k)))
		fmt.Fprintf(&b, "    static let %s = CopyKey(rawValue: %q)\n\n", swiftIdent(camel(strings.ReplaceAll(k.Key, ".", "_"))), k.Key)
	}
	b.WriteString("    /// Every key in the catalogue. `CopyTests` asserts each one resolves.\n    static let allKeys: [CopyKey] = [\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "        .%s,\n", camel(strings.ReplaceAll(k.Key, ".", "_")))
	}
	b.WriteString("    ]\n}\n")
	return b.String()
}

func emitKotlinCopy(keys []CopyKey) string {
	var b strings.Builder
	fmt.Fprintf(&b, banner, "content/microcopy.json")
	b.WriteString("//\n// Every user-facing string in the app is looked up through one of these keys.\n// A literal in a composable is a bug: see CLAUDE.md §2 rule 1.\n\n")
	fmt.Fprintf(&b, "package %s\n\n", kotlinPackage)
	b.WriteString("/** Keys into `content/microcopy.json`. */\npublic object CopyKey {\n")
	for _, k := range keys {
		b.WriteString(kdoc("    ", copyDoc(k)))
		fmt.Fprintf(&b, "    public const val %s: String = %q\n\n", screamingSnake(strings.ReplaceAll(k.Key, ".", "_")), k.Key)
	}
	b.WriteString("    /** Every key in the catalogue. `CopyTest` asserts each one resolves. */\n    public val all: List<String> = listOf(\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "        %s,\n", screamingSnake(strings.ReplaceAll(k.Key, ".", "_")))
	}
	b.WriteString("    )\n}\n")
	return b.String()
}

// --------------------------------------------------------------------- tokens

type Tokens struct {
	Colors  []ColorTokenDef
	Spacing []NumberToken
	Radius  []NumberToken
	Size    []NumberToken
	Motion  []NumberToken
	Opacity []NumberToken
	TypeSet []TypeToken
}

type ColorTokenDef struct {
	Name              string
	Role              string
	LightHex, DarkHex string
	LightAlpha        float64
	DarkAlpha         float64
}

type NumberToken struct {
	Name  string
	Value float64
}

type TypeToken struct {
	Name             string
	Size, LineHeight float64
	Weight           int
	Tracking         float64
	Role             string
}

type rawColor struct {
	Value string   `json:"value"`
	Alpha *float64 `json:"alpha"`
	Role  string   `json:"role"`
}

type rawType struct {
	Size       float64 `json:"size"`
	LineHeight float64 `json:"lineHeight"`
	Weight     int     `json:"weight"`
	Tracking   float64 `json:"tracking"`
	Role       string  `json:"role"`
}

func loadTokens(path string) (*Tokens, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	_, top, err := objectFields(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	t := &Tokens{}

	_, colorGroups, err := objectFields(top["color"])
	if err != nil {
		return nil, err
	}
	lightKeys, light, err := objectFields(colorGroups["light"])
	if err != nil {
		return nil, err
	}
	_, dark, err := objectFields(colorGroups["dark"])
	if err != nil {
		return nil, err
	}
	for _, name := range lightKeys {
		var l, d rawColor
		if err := json.Unmarshal(light[name], &l); err != nil {
			return nil, fmt.Errorf("color.light.%s: %w", name, err)
		}
		darkRaw, ok := dark[name]
		if !ok {
			return nil, fmt.Errorf("color.dark.%s: missing — every colour needs both schemes", name)
		}
		if err := json.Unmarshal(darkRaw, &d); err != nil {
			return nil, fmt.Errorf("color.dark.%s: %w", name, err)
		}
		def := ColorTokenDef{Name: name, Role: l.Role, LightHex: l.Value, DarkHex: d.Value, LightAlpha: 1, DarkAlpha: 1}
		if l.Alpha != nil {
			def.LightAlpha = *l.Alpha
		}
		if d.Alpha != nil {
			def.DarkAlpha = *d.Alpha
		}
		t.Colors = append(t.Colors, def)
	}

	numbers := func(raw json.RawMessage) ([]NumberToken, error) {
		keys, byKey, err := objectFields(raw)
		if err != nil {
			return nil, err
		}
		var out []NumberToken
		for _, k := range keys {
			var v float64
			if err := json.Unmarshal(byKey[k], &v); err != nil {
				continue // skip non-numeric leaves such as the `rule` notes
			}
			out = append(out, NumberToken{Name: k, Value: v})
		}
		return out, nil
	}

	if t.Spacing, err = numbers(top["spacing"]); err != nil {
		return nil, err
	}
	if t.Radius, err = numbers(top["radius"]); err != nil {
		return nil, err
	}
	if t.Size, err = numbers(top["size"]); err != nil {
		return nil, err
	}
	if t.Opacity, err = numbers(top["opacity"]); err != nil {
		return nil, err
	}
	_, motion, err := objectFields(top["motion"])
	if err != nil {
		return nil, err
	}
	if t.Motion, err = numbers(motion["duration"]); err != nil {
		return nil, err
	}

	_, typography, err := objectFields(top["typography"])
	if err != nil {
		return nil, err
	}
	scaleKeys, scale, err := objectFields(typography["scale"])
	if err != nil {
		return nil, err
	}
	for _, k := range scaleKeys {
		var r rawType
		if err := json.Unmarshal(scale[k], &r); err != nil {
			return nil, fmt.Errorf("typography.scale.%s: %w", k, err)
		}
		t.TypeSet = append(t.TypeSet, TypeToken{Name: k, Size: r.Size, LineHeight: r.LineHeight, Weight: r.Weight, Tracking: r.Tracking, Role: r.Role})
	}
	return t, nil
}

func num(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%g", v)
}

// decimal always renders a fractional part, for languages where `0` is not a Double.
func decimal(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d.0", int64(v))
	}
	return fmt.Sprintf("%g", v)
}

func emitSwiftTokens(t *Tokens) string {
	var b strings.Builder
	fmt.Fprintf(&b, banner, "design/tokens.json")
	b.WriteString(`//
// Design tokens, shared with Android by value and never by code.
// A raw hex or magic number in a view is a bug: add the token first.

import Foundation

public enum PodColorScheme: Sendable {
    case light
    case dark
}

/// A colour as authored: a 24-bit RGB value plus an opacity.
public struct PodColorValue: Hashable, Sendable {
    public let rgb: UInt32
    public let opacity: Double

    public init(rgb: UInt32, opacity: Double) {
        self.rgb = rgb
        self.opacity = opacity
    }

    public var red: Double { Double((rgb >> 16) & 0xFF) / 255 }
    public var green: Double { Double((rgb >> 8) & 0xFF) / 255 }
    public var blue: Double { Double(rgb & 0xFF) / 255 }
}

public struct PodTypeStyle: Hashable, Sendable {
    public let size: Double
    public let lineHeight: Double
    public let weight: Int
    public let tracking: Double
}

public enum PodTokens {
    /// Named colour roles. There is deliberately no error/danger role — see
    /// design/tokens.json and CLAUDE.md §2 rule 1.
    public enum ColorRole: String, CaseIterable, Sendable {
`)
	for _, c := range t.Colors {
		fmt.Fprintf(&b, "        case %s = %q\n", swiftIdent(camel(strings.ReplaceAll(c.Name, ".", "_"))), c.Name)
	}
	b.WriteString("    }\n\n    public static func color(_ role: ColorRole, _ scheme: PodColorScheme) -> PodColorValue {\n        switch (role, scheme) {\n")
	for _, c := range t.Colors {
		id := camel(strings.ReplaceAll(c.Name, ".", "_"))
		fmt.Fprintf(&b, "        case (.%s, .light): return PodColorValue(rgb: 0x%s, opacity: %s)\n", id, strings.TrimPrefix(c.LightHex, "#"), num(c.LightAlpha))
		fmt.Fprintf(&b, "        case (.%s, .dark): return PodColorValue(rgb: 0x%s, opacity: %s)\n", id, strings.TrimPrefix(c.DarkHex, "#"), num(c.DarkAlpha))
	}
	b.WriteString("        }\n    }\n\n")

	swiftNumberGroup(&b, "Spacing", "Multiples of the 4pt base unit.", t.Spacing, 1)
	swiftNumberGroup(&b, "Radius", "Corner radii.", t.Radius, 1)
	swiftNumberGroup(&b, "Size", "Fixed sizes, in points.", t.Size, 1)
	swiftNumberGroup(&b, "Opacity", "", t.Opacity, 1)
	swiftNumberGroup(&b, "Duration", "Animation durations, in seconds — the iOS-native unit.", t.Motion, 1000)

	b.WriteString("    /// Dynamic Type baselines. Never pin these — they scale.\n    public enum TypeScale {\n")
	for _, ty := range t.TypeSet {
		if ty.Role != "" {
			b.WriteString(docComment("        ", "/// ", ty.Role))
		}
		fmt.Fprintf(&b, "        public static let %s = PodTypeStyle(size: %s, lineHeight: %s, weight: %d, tracking: %s)\n", swiftIdent(camel(ty.Name)), num(ty.Size), num(ty.LineHeight), ty.Weight, num(ty.Tracking))
	}
	b.WriteString("    }\n}\n")
	return b.String()
}

func swiftNumberGroup(b *strings.Builder, name, doc string, tokens []NumberToken, divisor float64) {
	if doc != "" {
		b.WriteString(docComment("    ", "/// ", doc))
	}
	fmt.Fprintf(b, "    public enum %s {\n", name)
	for _, tok := range tokens {
		fmt.Fprintf(b, "        public static let %s: Double = %s\n", swiftIdent(camel(tok.Name)), num(tok.Value/divisor))
	}
	b.WriteString("    }\n\n")
}

func emitKotlinTokens(t *Tokens) string {
	var b strings.Builder
	fmt.Fprintf(&b, banner, "design/tokens.json")
	b.WriteString("//\n// Design tokens, shared with iOS by value and never by code.\n// A raw hex or magic number in a composable is a bug: add the token first.\n\n")
	fmt.Fprintf(&b, "package %s\n\n", kotlinPackage)
	b.WriteString(`/** A colour as authored: 0xAARRGGBB, with the token opacity already folded in. */
public typealias PodColorArgb = Long

public data class PodTypeStyle(
    val size: Int,
    val lineHeight: Int,
    val weight: Int,
    val tracking: Double,
)

public object PodTokens {
    /**
     * Named colour roles. There is deliberately no error/danger role — see
     * design/tokens.json and CLAUDE.md §2 rule 1.
     */
    public enum class ColorRole(public val wire: String) {
`)
	for _, c := range t.Colors {
		fmt.Fprintf(&b, "        %s(%q),\n", screamingSnake(strings.ReplaceAll(c.Name, ".", "_")), c.Name)
	}
	b.WriteString("        ;\n    }\n\n    public fun color(role: ColorRole, dark: Boolean): PodColorArgb =\n        if (dark) darkColors.getValue(role) else lightColors.getValue(role)\n\n")

	writeArgbMap(&b, "lightColors", t.Colors, false)
	writeArgbMap(&b, "darkColors", t.Colors, true)

	kotlinNumberGroup(&b, "Spacing", "Multiples of the 4dp base unit.", t.Spacing, "Int")
	kotlinNumberGroup(&b, "Radius", "Corner radii, in dp.", t.Radius, "Int")
	kotlinNumberGroup(&b, "Size", "Fixed sizes, in dp.", t.Size, "Int")
	kotlinNumberGroup(&b, "Opacity", "", t.Opacity, "Float")
	kotlinNumberGroup(&b, "DurationMs", "Animation durations, in milliseconds — the Android-native unit.", t.Motion, "Int")

	b.WriteString("    /** Font-scale baselines, in sp. Never pin these — they scale. */\n    public object TypeScale {\n")
	for _, ty := range t.TypeSet {
		if ty.Role != "" {
			b.WriteString(kdoc("        ", ty.Role))
		}
		fmt.Fprintf(&b, "        public val %s: PodTypeStyle = PodTypeStyle(size = %d, lineHeight = %d, weight = %d, tracking = %s)\n", screamingSnake(ty.Name), int(ty.Size), int(ty.LineHeight), ty.Weight, decimal(ty.Tracking))
	}
	b.WriteString("    }\n}\n")
	return b.String()
}

func writeArgbMap(b *strings.Builder, name string, colors []ColorTokenDef, dark bool) {
	fmt.Fprintf(b, "    private val %s: Map<ColorRole, PodColorArgb> = mapOf(\n", name)
	for _, c := range colors {
		hex, alpha := c.LightHex, c.LightAlpha
		if dark {
			hex, alpha = c.DarkHex, c.DarkAlpha
		}
		a := int(alpha*255 + 0.5)
		fmt.Fprintf(b, "        ColorRole.%s to 0x%02X%sL,\n", screamingSnake(strings.ReplaceAll(c.Name, ".", "_")), a, strings.ToUpper(strings.TrimPrefix(hex, "#")))
	}
	b.WriteString("    )\n\n")
}

func kotlinNumberGroup(b *strings.Builder, name, doc string, tokens []NumberToken, kind string) {
	if doc != "" {
		b.WriteString(kdoc("    ", doc))
	}
	fmt.Fprintf(b, "    public object %s {\n", name)
	for _, tok := range tokens {
		value := num(tok.Value)
		if kind == "Float" {
			value = fmt.Sprintf("%gf", tok.Value)
		}
		fmt.Fprintf(b, "        public const val %s: %s = %s\n", screamingSnake(tok.Name), kind, value)
	}
	b.WriteString("    }\n\n")
}

// emitGoCopy gives the server the same catalogue the clients ship, as typed keys plus
// the strings themselves. Push and email bodies are resolved here (ADR-003), and a
// handler physically cannot name a key that doesn't exist.
func emitGoCopy(keys []CopyKey, pluralForms map[string]map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, banner, "content/microcopy.json")
	b.WriteString(`//
// Every user-facing string the server can produce — error copy keys, email bodies,
// push bodies. A literal sentence in a handler is a bug: see CLAUDE.md §2 rule 1.

package copy

// Key indexes the catalogue. It is a distinct type so that a handler cannot pass an
// arbitrary string where a copy key belongs.
type Key string

const (
`)
	for _, k := range keys {
		b.WriteString(docComment("\t", "// ", copyDoc(k)))
		fmt.Fprintf(&b, "\tKey%s Key = %q\n\n", goPascal(strings.ReplaceAll(k.Key, ".", "_")), k.Key)
	}
	b.WriteString(")\n\n// AllKeys is every key in the catalogue, in file order.\nvar AllKeys = []Key{\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "\tKey%s,\n", goPascal(strings.ReplaceAll(k.Key, ".", "_")))
	}
	b.WriteString("}\n\n// catalogue holds the en-US strings. Plural keys carry one entry per CLDR category.\nvar catalogue = map[Key]map[string]string{\n")
	for _, k := range keys {
		forms := pluralForms[k.Key]
		if forms == nil {
			forms = map[string]string{"other": k.Sample}
		}
		fmt.Fprintf(&b, "\tKey%s: {", goPascal(strings.ReplaceAll(k.Key, ".", "_")))
		formKeys := make([]string, 0, len(forms))
		for f := range forms {
			formKeys = append(formKeys, f)
		}
		sortStrings(formKeys)
		for i, f := range formKeys {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%q: %q", f, forms[f])
		}
		b.WriteString("},\n")
	}
	b.WriteString("}\n")
	return b.String()
}
