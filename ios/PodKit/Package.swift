// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "PodKit",
    // macOS is here so `swift test` runs without a simulator; the app itself is iOS 17+.
    platforms: [.iOS(.v17), .macOS(.v14)],
    products: [
        .library(name: "PodAPI", targets: ["PodAPI"]),
    ],
    targets: [
        .target(
            name: "PodAPI",
            resources: [.process("Resources")],
            swiftSettings: [.swiftLanguageMode(.v6)]
        ),
        .testTarget(
            name: "PodAPITests",
            dependencies: ["PodAPI"],
            swiftSettings: [.swiftLanguageMode(.v6)]
        ),
    ]
)
