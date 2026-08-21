import Foundation

/// Resolves a ``CopyKey`` into the string a person actually reads.
///
/// The catalogue is `content/microcopy.json`, copied into this module's bundle by
/// `make generate`. Nothing in the app builds a user-facing string any other way — see
/// CLAUDE.md §2 rule 1.
public struct Copywriter: Sendable {
    private let strings: [String: Entry]

    private enum Entry: Sendable {
        case single(String)
        case plural([String: String])
    }

    /// The catalogue shipped with the app.
    public static let bundled: Copywriter = {
        do {
            return try Copywriter(bundle: .module)
        } catch {
            // The catalogue is a build product, not user input. If it is missing or
            // malformed the app cannot render a single screen correctly, and shipping a
            // silent fallback would hide that until it reached someone.
            preconditionFailure("the microcopy catalogue failed to load: \(error)")
        }
    }()

    public init(bundle: Bundle) throws {
        guard let url = bundle.url(forResource: "microcopy", withExtension: "json") else {
            throw CopywriterError.catalogueMissing
        }
        try self.init(data: Data(contentsOf: url))
    }

    public init(data: Data) throws {
        guard let raw = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            throw CopywriterError.catalogueMalformed
        }
        var strings: [String: Entry] = [:]
        for (key, value) in raw where !key.hasPrefix("$") {
            switch value {
            case let text as String:
                strings[key] = .single(text)
            case let forms as [String: String]:
                strings[key] = .plural(forms)
            default:
                throw CopywriterError.catalogueMalformed
            }
        }
        self.strings = strings
    }

    /// Returns the string for `key`, substituting `{placeholders}`.
    public func callAsFunction(_ key: CopyKey, _ params: [String: String] = [:]) -> String {
        resolve(key, count: 1, params: params)
    }

    /// Returns the plural form appropriate to `count`.
    public func plural(_ key: CopyKey, count: Int, _ params: [String: String] = [:]) -> String {
        var params = params
        params["count"] = String(count)
        return resolve(key, count: count, params: params)
    }

    public func contains(_ key: CopyKey) -> Bool {
        strings[key.rawValue] != nil
    }

    /// The catalogue string with its `{placeholders}` intact, or `nil` if absent.
    ///
    /// For tests that assert on the *shape* of a string — that it interpolates a value
    /// rather than spelling it out, that a deadline is a date and not a countdown. A
    /// resolved string cannot answer either question.
    public func raw(_ key: CopyKey) -> String? {
        switch strings[key.rawValue] {
        case .single(let text): return text
        case .plural(let forms): return forms["other"] ?? forms.values.first
        case nil: return nil
        }
    }

    private func resolve(_ key: CopyKey, count: Int, params: [String: String]) -> String {
        guard let entry = strings[key.rawValue] else {
            // A missing key means the catalogue and the generated constants disagree,
            // which is a build problem. Loud in debug; blank rather than a raw key on a
            // screen in release.
            assertionFailure("missing copy key \(key.rawValue) — run `make generate`")
            return ""
        }
        let template: String
        switch entry {
        case .single(let text):
            template = text
        case .plural(let forms):
            template = forms[count == 1 ? "one" : "other"] ?? forms["other"] ?? ""
        }
        return Self.interpolate(template, params)
    }

    static func interpolate(_ template: String, _ params: [String: String]) -> String {
        guard template.contains("{") else { return template }
        var out = ""
        var rest = Substring(template)
        while let open = rest.firstIndex(of: "{") {
            out += rest[rest.startIndex..<open]
            let afterOpen = rest.index(after: open)
            guard let close = rest[afterOpen...].firstIndex(of: "}") else {
                out += rest[open...]
                return out
            }
            let name = String(rest[afterOpen..<close])
            out += params[name] ?? ""
            rest = rest[rest.index(after: close)...]
        }
        return out + rest
    }
}

public enum CopywriterError: Error, Sendable {
    case catalogueMissing
    case catalogueMalformed
}
