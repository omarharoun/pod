#if canImport(Security)
import Foundation
import Security

/// Tokens in the Keychain, as CLAUDE.md §5 requires.
///
/// One item holds the whole session, because the access token and the refresh token are
/// only ever written together — two items could disagree after a crash between writes.
public struct KeychainTokenStore: TokenStore {
    private let service: String
    private let account: String

    public init(service: String = "app.pod.session", account: String = "current") {
        self.service = service
        self.account = account
    }

    public func load() throws -> StoredSession? {
        var query = baseQuery()
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne

        var item: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &item)
        switch status {
        case errSecSuccess:
            guard let data = item as? Data else { throw KeychainError.unexpectedFormat }
            return try JSONDecoder().decode(StoredSession.self, from: data)
        case errSecItemNotFound:
            return nil
        default:
            throw KeychainError.status(status)
        }
    }

    public func save(_ session: StoredSession) throws {
        let data = try JSONEncoder().encode(session)
        let query = baseQuery()
        let attributes: [String: Any] = [kSecValueData as String: data]

        let updated = SecItemUpdate(query as CFDictionary, attributes as CFDictionary)
        if updated == errSecSuccess { return }
        guard updated == errSecItemNotFound else { throw KeychainError.status(updated) }

        var insert = query
        insert[kSecValueData as String] = data
        // The tokens are only useful while the device is unlocked, and they must not
        // travel to a restored backup on another device.
        insert[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        let added = SecItemAdd(insert as CFDictionary, nil)
        guard added == errSecSuccess else { throw KeychainError.status(added) }
    }

    public func clear() throws {
        let status = SecItemDelete(baseQuery() as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw KeychainError.status(status)
        }
    }

    private func baseQuery() -> [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
    }
}

public enum KeychainError: Error, Sendable {
    case status(OSStatus)
    case unexpectedFormat
}
#endif
