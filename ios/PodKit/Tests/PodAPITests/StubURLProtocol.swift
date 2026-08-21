import Foundation

/// A `URLProtocol` that answers from a scripted queue, so the client can be tested
/// without a server. Requests are recorded in order for assertions.
final class StubURLProtocol: URLProtocol, @unchecked Sendable {
    struct Reply {
        var status: Int
        var body: Data
    }

    /// One recorded request. The body is captured here rather than read back off the
    /// `URLRequest`: by the time a request reaches a protocol, `URLSession` has moved the
    /// body into `httpBodyStream` and `httpBody` is nil.
    struct Recorded {
        var request: URLRequest
        var body: Data?

        var method: String { request.httpMethod ?? "GET" }
        var path: String { request.url?.path ?? "" }

        /// The body decoded as a JSON object, for asserting on what was actually sent.
        func json() -> [String: Any]? {
            guard let body else { return nil }
            return try? JSONSerialization.jsonObject(with: body) as? [String: Any]
        }
    }

    private static let lock = NSLock()
    nonisolated(unsafe) private static var replies: [Reply] = []
    nonisolated(unsafe) private static var recorded: [Recorded] = []

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
        lock.withLock { recorded.map(\.request) }
    }

    /// Every request in order, with its body.
    static var calls: [Recorded] {
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
        let body = Self.readBody(of: request)
        let reply: Reply? = Self.lock.withLock {
            Self.recorded.append(Recorded(request: request, body: body))
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

    private static func readBody(of request: URLRequest) -> Data? {
        if let body = request.httpBody { return body }
        guard let stream = request.httpBodyStream else { return nil }
        stream.open()
        defer { stream.close() }
        var data = Data()
        var buffer = [UInt8](repeating: 0, count: 4096)
        while stream.hasBytesAvailable {
            let read = stream.read(&buffer, maxLength: buffer.count)
            if read <= 0 { break }
            data.append(buffer, count: read)
        }
        return data.isEmpty ? nil : data
    }
}
