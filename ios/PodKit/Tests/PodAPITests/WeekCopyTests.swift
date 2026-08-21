import XCTest
@testable import PodAPI

/// The week screen is where CLAUDE.md §2 rule 1 is most exposed: it is the one surface
/// that shows people to each other every week, including the weeks somebody sat out.
final class WeekCopyTests: XCTestCase {
    private let copy = Copywriter.bundled

    func testEveryMemberStateHasCopyAndAnAccessibilityLabel() {
        for state in MemberWeekState.knownCases {
            let short = CopyKey(rawValue: "state.member.\(state.rawValue).short")
            let detail = CopyKey(rawValue: "state.member.\(state.rawValue).detail")
            XCTAssertTrue(copy.contains(short), "no short label for \(state.rawValue)")
            XCTAssertTrue(copy.contains(detail), "no detail for \(state.rawValue)")
            XCTAssertTrue(
                copy.contains(CopyKey(rawValue: "a11y.member\(state.rawValue.capitalized)")),
                "no accessibility label for \(state.rawValue)"
            )
        }
    }

    // `resting` is the state a whole product rule exists to protect. It must not read as a
    // verdict anywhere it is rendered.
    func testRestingNeverReadsAsAVerdict() {
        let keys = [
            CopyKey.stateMemberRestingShort,
            .stateMemberRestingDetail,
            .stateMemberYouRestingDetail,
            .a11yMemberResting,
        ]
        for key in keys {
            let text = copy(key, ["name": "Alex"]).lowercased()
            for verdict in ["missed", "failed", "didn't", "did not", "behind", "broke", "lost", "0"] {
                XCTAssertFalse(text.contains(verdict), "\(key.rawValue) reads as a verdict: \(text)")
            }
        }
    }

    // Nothing on this screen may congratulate one person over another.
    func testCompletionCopyIsAboutThePod() {
        for key in [CopyKey.weekPodCompleted, .weekPodPartial, .statePodRestartingNote] {
            let text = copy(key).lowercased()
            XCTAssertFalse(text.contains("you "), "\(key.rawValue) singles someone out: \(text)")
            XCTAssertFalse(text.contains("!"), "\(key.rawValue) is celebrating: \(text)")
        }
        XCTAssertTrue(copy(.weekPodCompleted).lowercased().contains("everyone"))
    }

    // The promise a person is deciding whether to believe, at the moment they decide.
    func testThePostingSheetStatesTheEphemeralityPromise() {
        let note = copy(.weekPostEphemeralNote).lowercased()
        XCTAssertTrue(note.contains("disappear"), "the note does not say the proof goes away: \(note)")
        XCTAssertTrue(note.contains("pod"), "the note does not say who can see it: \(note)")
    }
}
