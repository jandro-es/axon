// axon-apple-lm — AXON's Apple Foundation Models helper (ADR-015).
// Compiled by `axon init` from source embedded in the axon binary.
// Protocol: stdin {"system":..., "prompt":..., "max_tokens":..., "schema":...?}
//        → stdout {"text": ...}.
// --check-availability: exit 0 if the on-device model is available, 3 if not.
// Errors: message on stderr + non-zero exit (2 decode, 3 unavailable,
// 4 context overflow, 5 guardrail refusal, 6 generation error, 7 encode).
import Foundation
import FoundationModels

struct Request: Decodable {
    let system: String?
    let prompt: String
    let max_tokens: Int?
    let schema: SchemaSpec?
}
// Flat-object schema subset: string and [string] properties. Enough for the
// classify-tier callers (enrichment metadata, triage labels); anything richer
// falls back to plain text + Go-side validation.
struct SchemaSpec: Decodable {
    struct Property: Decodable { let type: String }
    let properties: [String: Property]?
}
struct Reply: Encodable { let text: String }

func fail(_ msg: String, code: Int32) -> Never {
    FileHandle.standardError.write((msg + "\n").data(using: .utf8)!)
    exit(code)
}

let model = SystemLanguageModel.default

if CommandLine.arguments.contains("--check-availability") {
    switch model.availability {
    case .available:
        print("available")
        exit(0)
    case .unavailable(let reason):
        fail("on-device model unavailable: \(reason) — requires Apple Silicon, macOS 26+, Apple Intelligence enabled", code: 3)
    }
}

guard case .available = model.availability else {
    fail("on-device model unavailable — run `axon doctor` for details", code: 3)
}

let input = FileHandle.standardInput.readDataToEndOfFile()
let req: Request
do { req = try JSONDecoder().decode(Request.self, from: input) } catch {
    fail("decode request: \(error.localizedDescription)", code: 2)
}

let sem = DispatchSemaphore(value: 0)
var replyText: String?
var failure: (String, Int32)?

Task {
    defer { sem.signal() }
    do {
        let session = LanguageModelSession(instructions: req.system ?? "")
        var options = GenerationOptions()
        options.maximumResponseTokens = req.max_tokens ?? 1024

        if let props = req.schema?.properties, !props.isEmpty {
            // Guided generation from the flat schema subset.
            var fields: [DynamicGenerationSchema.Property] = []
            for (name, p) in props.sorted(by: { $0.key < $1.key }) {
                let child: DynamicGenerationSchema = p.type == "array"
                    ? DynamicGenerationSchema(arrayOf: DynamicGenerationSchema(type: String.self))
                    : DynamicGenerationSchema(type: String.self)
                fields.append(.init(name: name, schema: child))
            }
            let root = DynamicGenerationSchema(name: "Output", properties: fields)
            let schema = try GenerationSchema(root: root, dependencies: [])
            let resp = try await session.respond(to: req.prompt, schema: schema, options: options)
            replyText = resp.content.jsonString
        } else {
            let resp = try await session.respond(to: req.prompt, options: options)
            replyText = resp.content
        }
    } catch let e as LanguageModelSession.GenerationError {
        switch e {
        case .exceededContextWindowSize:
            failure = ("input exceeds the on-device context window", 4)
        case .guardrailViolation:
            failure = ("request declined by on-device guardrails", 5)
        default:
            failure = ("generation error: \(e.localizedDescription)", 6)
        }
    } catch {
        failure = ("generation error: \(error.localizedDescription)", 6)
    }
}
sem.wait()

if let (msg, code) = failure { fail(msg, code: code) }
guard let text = replyText,
      let out = try? JSONEncoder().encode(Reply(text: text)) else {
    fail("encode response", code: 7)
}
FileHandle.standardOutput.write(out)
