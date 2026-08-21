import XCTest
@testable import PodAPI

final class CopywriterTests: XCTestCase {
    private let copy = Copywriter.bundled

    // The generated constants and the bundled catalogue come from the same file. If they
    // ever disagree, every screen using the missing key renders blank.
    func testEveryGeneratedKeyResolves() {
        for key in CopyKey.allKeys {
            XCTAssertTrue(copy.contains(key), "missing copy for \(key.rawValue)")
            XCTAssertFalse(copy(key, placeholders(for: key)).isEmpty, "\(key.rawValue) resolved to nothing")
        }
    }

    func testInterpolation() {
        XCTAssertEqual(
            Copywriter.interpolate("Signed in as {email}", ["email": "sam@example.com"]),
            "Signed in as sam@example.com"
        )
        XCTAssertEqual(
            Copywriter.interpolate("{a} and {b} and {a}", ["a": "one", "b": "two"]),
            "one and two and one"
        )
        XCTAssertEqual(Copywriter.interpolate("nothing to fill", [:]), "nothing to fill")
        XCTAssertEqual(Copywriter.interpolate("unclosed {brace", [:]), "unclosed {brace")
    }

    func testPluralFormsDiffer() {
        let one = copy.plural(.statePodWeeksTogether, count: 1)
        let many = copy.plural(.statePodWeeksTogether, count: 7)
        XCTAssertEqual(one, "1 week together")
        XCTAssertEqual(many, "7 weeks together")
    }

    // CLAUDE.md §2 rule 1, checked on the client too: tools/copylint guards the catalogue
    // in CI, and this guards the copy that actually ships inside the app bundle.
    func testNoShamingVocabularyShipsInTheBundle() {
        let forbidden = ["failed", "failure", "you missed", "streak broken", "don't let",
                         "let your pod down", "lazy", "0%", "penalty", "leaderboard"]
        for key in CopyKey.allKeys {
            let text = copy(key, placeholders(for: key)).lowercased()
            for word in forbidden {
                XCTAssertFalse(text.contains(word), "\(key.rawValue) contains \"\(word)\": \(text)")
            }
            XCTAssertFalse(text.contains("!"), "\(key.rawValue) is shouting: \(text)")
        }
    }

    func testMalformedCatalogueIsRejected() {
        XCTAssertThrowsError(try Copywriter(data: Data("not json".utf8)))
        XCTAssertThrowsError(try Copywriter(data: Data(#"{"key": 42}"#.utf8)))
    }

    /// Every placeholder any key might use. Passing all of them keeps this test about
    /// resolution rather than about which key takes what.
    private func placeholders(for key: CopyKey) -> [String: String] {
        ["email": "sam@example.com", "minutes": "10", "seconds": "30", "count": "3",
         "name": "Ada", "names": "Ada and Sam", "code": "482913",
         "action": "Clear one shelf", "time": "10:00", "label": "Half an hour",
         "index": "2", "total": "3"]
    }
}
