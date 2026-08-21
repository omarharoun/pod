import Foundation

/// A `URLProtocol` that answers from a scripted queue, so the client can be tested
/// without a server. Requests are recorded in order for assertions.
final class StubURLProtocol: URLProtocol, @unchecked Sendable {
    struct Reply {
        var status: Int
        var body: Data
    }

    private static let lock = NSLock()
    nonisolated(unsafe) private static var replies: [Reply] = []
    nonisolated(unsafe) private static var recorded: [URLRequest] = []

    static func reset() {
        lock.withLock {
            replies = []
            recorded = []
        }
    }

    static func enqueue(status: Int = 200, json: String) {
        lock.withLock { replies.append(Reply(status: status, body: Data(json.utf8))) }
    }

    static var requests: [URLRequest] {
        lock.withLock { recorded }
    }

    static func session() -> URLSession {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [StubURLProtocol.self]
        return URLSession(configuration: configuration)
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let reply: Reply? = Self.lock.withLock {
            Self.recorded.append(request)
            return Self.replies.isEmpty ? nil : Self.replies.removeFirst()
        }
        guard let reply else {
            client?.urlProtocol(self, didFailWithError: URLError(.badServerResponse))
            return
        }
        let response = HTTPURLResponse(
            url: request.url!,
            statusCode: reply.status,
            httpVersion: "HTTP/1.1",
            headerFields: ["Content-Type": "application/json"]
        )!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: reply.body)
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}
