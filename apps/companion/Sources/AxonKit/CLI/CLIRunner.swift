import Foundation

/// One completed `axon` invocation.
public struct CLIResult: Sendable {
    public let exitCode: Int32
    public let stdout: Data
    public let stderr: String

    public var stdoutText: String { String(decoding: stdout, as: UTF8.self) }
    public var succeeded: Bool { exitCode == 0 }
}

/// What can go wrong running the CLI.
public enum AxonCLIError: Error, Sendable {
    /// The command exited non-zero. `stderr` carries the daemon's own message,
    /// which is what the user should see — Companion never rewrites it.
    case failed(command: String, stderr: String, exitCode: Int32)
    /// The command outlived its deadline and was killed.
    case timedOut(command: String)
    /// The binary is missing or not executable.
    case notExecutable(path: String)
    /// The command succeeded but its `--json` output did not decode.
    case decoding(command: String, detail: String)

    /// A single line fit to show a user.
    public var message: String {
        switch self {
        case .failed(_, let stderr, let code):
            let cleaned = Self.clean(stderr)
            return cleaned.isEmpty ? "exited with code \(code)" : cleaned
        case .timedOut(let command):
            return "`axon \(command)` did not respond in time"
        case .notExecutable(let path):
            return "cannot run \(path)"
        case .decoding(let command, let detail):
            return "could not read `axon \(command) --json`: \(detail)"
        }
    }

    /// Strips the CLI's styled `✗ Error:` prefix and surrounding blank lines so
    /// the message reads naturally inside a Companion alert.
    private static func clean(_ stderr: String) -> String {
        stderr
            .replacingOccurrences(of: "✗ Error:", with: "")
            .trimmingCharacters(in: .whitespacesAndNewlines)
    }
}

/// Runs a subprocess. Abstracted so `AxonCLI` is testable without spawning.
public protocol CLIRunning: Sendable {
    func run(binary: URL, arguments: [String], timeout: Duration) async throws -> CLIResult
    /// Spawn without waiting. For `axon start`, which never returns.
    func spawnDetached(binary: URL, arguments: [String]) throws
}

/// `Process`-backed runner.
public struct ProcessCLIRunner: CLIRunning {
    /// Extra environment for the child. Tests use it to drive the fake binary;
    /// production passes nothing.
    private let environment: [String: String]

    public init(environment: [String: String] = [:]) {
        self.environment = environment
    }

    public func run(binary: URL, arguments: [String], timeout: Duration) async throws -> CLIResult {
        let command = arguments.joined(separator: " ")

        guard FileManager.default.isExecutableFile(atPath: binary.path) else {
            throw AxonCLIError.notExecutable(path: binary.path)
        }

        let process = Process()
        process.executableURL = binary
        process.arguments = arguments
        // Never `$SHELL -c`: no shell means no quoting bugs and no user rc
        // files changing what runs (CONTRACT.md §10).
        process.environment = ProcessInfo.processInfo.environment.merging(environment) { _, new in new }

        let outPipe = Pipe()
        let errPipe = Pipe()
        process.standardOutput = outPipe
        process.standardError = errPipe

        do {
            try process.run()
        } catch {
            throw AxonCLIError.notExecutable(path: binary.path)
        }

        // Read both pipes concurrently. Draining only after exit deadlocks as
        // soon as a command's output exceeds the pipe buffer — `axon
        // automations --json` is already 13 KB.
        async let stdout = Self.readToEnd(outPipe)
        async let stderr = Self.readToEnd(errPipe)

        // Race exit against the deadline. `withTaskGroup` awaits every child
        // before it returns, so the waiter MUST be cancellable — otherwise
        // cancelAll() is a no-op and the group blocks for the child's full
        // lifetime, which is precisely the case the timeout exists to prevent.
        let timedOut = await withTaskGroup(of: Bool.self) { group in
            group.addTask {
                await Self.waitForExit(process, cancelling: { Self.kill(process) })
                return false
            }
            group.addTask {
                try? await Task.sleep(for: timeout)
                return true
            }
            let first = await group.next() ?? false
            group.cancelAll()
            return first
        }

        if timedOut {
            throw AxonCLIError.timedOut(command: command)
        }

        return CLIResult(
            exitCode: process.terminationStatus,
            stdout: await stdout,
            stderr: String(decoding: await stderr, as: UTF8.self)
        )
    }

    public func spawnDetached(binary: URL, arguments: [String]) throws {
        guard FileManager.default.isExecutableFile(atPath: binary.path) else {
            throw AxonCLIError.notExecutable(path: binary.path)
        }
        let process = Process()
        process.executableURL = binary
        process.arguments = arguments
        process.environment = ProcessInfo.processInfo.environment.merging(environment) { _, new in new }
        // Detach every stream: an inherited pipe nobody drains would eventually
        // block the daemon on its own logging.
        process.standardOutput = FileHandle.nullDevice
        process.standardError = FileHandle.nullDevice
        process.standardInput = FileHandle.nullDevice
        try process.run()
    }

    private static func readToEnd(_ pipe: Pipe) async -> Data {
        await withCheckedContinuation { continuation in
            DispatchQueue.global(qos: .userInitiated).async {
                let data = (try? pipe.fileHandleForReading.readToEnd()) ?? Data()
                continuation.resume(returning: data)
            }
        }
    }

    /// Waits for exit, killing the child if the surrounding task is cancelled.
    ///
    /// Killing on cancel is what makes the wait cancellable at all: the
    /// continuation can only be resumed by the process actually terminating.
    private static func waitForExit(
        _ process: Process, cancelling onCancel: @escaping @Sendable () -> Void
    ) async {
        await withTaskCancellationHandler {
            // The process can exit between `run()` and installing the handler,
            // so both paths must be able to resume — but exactly once.
            // Resuming a continuation twice is a crash, not a warning.
            let resumed = OnceFlag()
            await withCheckedContinuation { continuation in
                process.terminationHandler = { _ in
                    if resumed.claim() { continuation.resume() }
                }
                if !process.isRunning, resumed.claim() {
                    continuation.resume()
                }
            }
        } onCancel: {
            onCancel()
        }
    }

    /// SIGTERM, then SIGKILL if the child ignores it. Nothing `axon` runs may
    /// outlive the request that started it.
    private static func kill(_ process: Process) {
        guard process.isRunning else { return }
        process.terminate()
        DispatchQueue.global().asyncAfter(deadline: .now() + 0.2) {
            if process.isRunning {
                Darwin.kill(process.processIdentifier, SIGKILL)
            }
        }
    }
}

/// A one-shot, thread-safe latch.
private final class OnceFlag: @unchecked Sendable {
    private let lock = NSLock()
    private var claimed = false

    /// Returns true to exactly one caller.
    func claim() -> Bool {
        lock.withLock {
            if claimed { return false }
            claimed = true
            return true
        }
    }
}
