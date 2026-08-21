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
    dependencies: [
        // The ONLY dependency in the app, and only the executable target sees
        // it: AxonKit stays dependency-free so its tests never pull a
        // networked package in.
        .package(url: "https://github.com/sparkle-project/Sparkle", from: "2.6.0")
    ],
    targets: [
        // All logic lives here: clients, CLI wrapper, state machines. No UI,
        // no SwiftUI import, 100% unit-testable.
        .target(name: "AxonKit"),

        // Views only. Reads @Observable models from AxonKit and renders.
        .executableTarget(
            name: "Companion",
            dependencies: ["AxonKit", .product(name: "Sparkle", package: "Sparkle")]
        ),

        // The share extension's appex executable (CFR-96). Views only — every
        // testable part lives in AxonKit.
        //
        // The linker flag is load-bearing: an appex's entry point is
        // NSExtensionMain, not main(), which is why this target has no
        // main.swift. It MUST be spelled through -Xlinker; passing "-e" to
        // swiftc directly makes it evaluate the symbol name as Swift source
        // ("cannot find '_NSExtensionMain' in scope"). unsafeFlags is legal
        // because Companion is a root package, never a dependency.
        .executableTarget(
            name: "AxonShare",
            dependencies: ["AxonKit"],
            linkerSettings: [.unsafeFlags(["-Xlinker", "-e", "-Xlinker", "_NSExtensionMain"])]
        ),

        .testTarget(
            name: "AxonKitTests",
            dependencies: ["AxonKit"],
            resources: [.copy("Fixtures")]
        ),
    ]
)
