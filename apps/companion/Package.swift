// swift-tools-version: 6.2
import PackageDescription

let package = Package(
    name: "Companion",
    platforms: [
        // macOS 26 is the floor (CFR-90). Liquid Glass and the MenuBarExtra
        // window style below it are not available, and Companion is a 26+
        // product by design — the CLI and web dashboard cover older machines.
        .macOS("26.0")
    ],
    targets: [
        // All logic lives here: clients, CLI wrapper, state machines. No UI,
        // no SwiftUI import, 100% unit-testable.
        .target(name: "AxonKit"),

        // Views only. Reads @Observable models from AxonKit and renders.
        .executableTarget(name: "Companion", dependencies: ["AxonKit"]),

        .testTarget(
            name: "AxonKitTests",
            dependencies: ["AxonKit"],
            resources: [.copy("Fixtures")]
        ),
    ]
)
