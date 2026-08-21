import XCTest
@testable import PodAPI

/// The onboarding screens build copy keys from contract enum values — a category row
/// looks up `onboarding.category.<value>`. That is convenient and it is exactly the kind
/// of link nothing else would check: adding a category to the spec without adding its
/// copy produces a blank row, not a build failure.
final class OnboardingCopyTests: XCTestCase {
    private let copy = Copywriter.bundled

    func testEveryGoalCategoryHasALabel() {
        for category in GoalCategory.knownCases {
            let key = CopyKey(rawValue: "onboarding.category.\(category.rawValue)")
            XCTAssertTrue(copy.contains(key), "no label for category \(category.rawValue)")
            XCTAssertFalse(copy(key).isEmpty, "empty label for category \(category.rawValue)")
        }
    }

    func testEverySelfReportAnswerHasALabel() {
        for answer in SelfReportReliability.knownCases {
            let key = CopyKey(rawValue: "onboarding.reliability.\(answer.rawValue)")
            XCTAssertTrue(copy.contains(key), "no label for self-report \(answer.rawValue)")
        }
    }

    func testEveryIntensityRungHasALabel() {
        for rung in 1...5 {
            let key = CopyKey(rawValue: "onboarding.intensity.\(rung)")
            XCTAssertTrue(copy.contains(key), "no label for intensity rung \(rung)")
        }
    }

    // ADR-010: a person picks a phrase, never a number, and never sees where they sit on
    // a scale relative to anyone else.
    func testIntensityLabelsCarryNoNumbersOrPositions() {
        for rung in 1...5 {
            let label = copy(CopyKey(rawValue: "onboarding.intensity.\(rung)"))
            XCTAssertFalse(label.contains(String(rung)), "rung \(rung) shows its number: \(label)")
            for marker in ["of 5", "level", "/5", "tier"] {
                XCTAssertFalse(label.lowercased().contains(marker), "rung \(rung) leaks a position: \(label)")
            }
        }
    }

    // The self-report screen is the most likely place in the product to make someone feel
    // judged before they have done anything, so the answers describe circumstances rather
    // than character.
    func testSelfReportAnswersDoNotJudge() {
        for answer in SelfReportReliability.knownCases {
            let label = copy(CopyKey(rawValue: "onboarding.reliability.\(answer.rawValue)")).lowercased()
            for judgement in ["rarely", "never", "bad", "poor", "unreliable", "flaky"] {
                XCTAssertFalse(label.contains(judgement), "\(answer.rawValue) reads as a verdict: \(label)")
            }
        }
    }
}

// MARK: - Recovery (M5)

/// The recovery screen is the sharpest test of CLAUDE.md §2 rule 1: it is shown to
/// someone whose pod just ended, and every true statement about why is a statement about
/// somebody not showing up.
final class RecoveryCopyTests: XCTestCase {
    private let copy = Copywriter.bundled

    private static let keys: [CopyKey] = [
        .recoveryPodEndedTitle, .recoveryPodEndedBody,
        .recoveryLowerBarTitle, .recoveryLowerBarBody,
        .recoveryLowerBarAccept, .recoveryLowerBarKeep,
        .recoveryFindAnother,
        .recoveryAlternateTitle, .recoveryAlternateBody, .recoveryAlternateNotYet,
    ]

    func testEveryStringOnTheScreenResolves() {
        for key in Self.keys {
            XCTAssertTrue(copy.contains(key), "missing \(key.rawValue)")
            XCTAssertFalse(copy(key).isEmpty, "empty \(key.rawValue)")
        }
    }

    /// It may not say why the pod ended, and it may not count. "Again", "still" and
    /// "unfortunately" all carry a history the copy is not allowed to have.
    func testNothingOnTheScreenAssignsBlameOrCounts() {
        let forbidden = [
            "again", "unfortunately", "failed", "fail", "missed", "inactive",
            "nobody", "no one", "didn't post", "gave up", "quit", "third", "second time",
        ]
        for key in Self.keys {
            let text = copy(key).lowercased()
            for word in forbidden {
                XCTAssertFalse(text.contains(word), "\(key.rawValue) says \"\(word)\": \(copy(key))")
            }
        }
    }

    /// Both rungs are named by phrase. Interpolating the raw integer would put someone on
    /// a scale, which ADR-010 forbids.
    func testTheOfferNamesBothRungsByPhrase() {
        let body = copy(.recoveryLowerBarBody, [
            "current": copy(CopyKey(rawValue: "onboarding.intensity.3")),
            "suggested": copy(CopyKey(rawValue: "onboarding.intensity.2")),
        ])
        XCTAssertTrue(body.contains("An hour or two"), body)
        XCTAssertTrue(body.contains("Half an hour"), body)
        XCTAssertFalse(body.contains("{"), "unresolved placeholder: \(body)")
    }

    func testTheAcceptButtonNamesTheSmallerRung() {
        let title = copy(.recoveryLowerBarAccept, ["suggested": copy(CopyKey(rawValue: "onboarding.intensity.1"))])
        XCTAssertTrue(title.contains("A few minutes"), title)
        XCTAssertFalse(title.contains("{"), "unresolved placeholder: \(title)")
    }

    /// The alternate path is offered as another way, never as a verdict about the person.
    func testTheAlternatePathIsNotAVerdict() {
        let text = (copy(.recoveryAlternateTitle) + " " + copy(.recoveryAlternateBody)).lowercased()
        for phrase in ["not for you", "instead of", "isn't working", "better suited"] {
            XCTAssertFalse(text.contains(phrase), "the alternate path reads as a verdict: \(text)")
        }
    }
}
