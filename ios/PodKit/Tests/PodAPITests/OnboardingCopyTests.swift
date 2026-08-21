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
