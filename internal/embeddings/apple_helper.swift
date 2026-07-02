// axon-apple-embed — AXON's Apple on-device embeddings helper.
// Compiled by `axon init` from source embedded in the axon binary.
// Protocol: stdin {"texts":[...]} → stdout {"model":..., "dim":..., "vectors":[[...]]}.
// Errors: message on stderr + non-zero exit.
import Foundation
import NaturalLanguage

struct Request: Decodable { let texts: [String] }
struct Response: Encodable { let model: String; let dim: Int; let vectors: [[Float]] }

func fail(_ msg: String, code: Int32) -> Never {
    FileHandle.standardError.write((msg + "\n").data(using: .utf8)!)
    exit(code)
}

let modelID = "apple-nlcontextual-v1"

guard let embedding = NLContextualEmbedding(language: .english) else {
    fail("no on-device contextual embedding model (requires macOS 14+)", code: 2)
}

// --check-assets: report availability WITHOUT downloading. Lets callers (the
// integration test, diagnostics) find out cheaply whether this machine can
// embed at all — CI runners and managed Macs often cannot fetch the assets.
if CommandLine.arguments.contains("--check-assets") {
    if embedding.hasAvailableAssets {
        print("assets available (dim \(embedding.dimension))")
        exit(0)
    }
    fail("assets not downloaded on this machine", code: 3)
}

if !embedding.hasAvailableAssets {
    let sem = DispatchSemaphore(value: 0)
    var assetErr: Error?
    embedding.requestAssets { _, error in assetErr = error; sem.signal() }
    // BOUNDED wait, shorter than every Go-side timeout: on machines that
    // cannot fetch the assets (CI, MDM-restricted), an unbounded wait hangs
    // silently until the caller SIGKILLs us — "signal: killed" with no clue.
    // Better to die on our own terms with a readable reason.
    if sem.wait(timeout: .now() + 75) == .timedOut {
        fail("embedding assets not downloadable within 75s (offline, CI, or managed Mac?) — run once with network access, or use embeddings.provider: ollama", code: 3)
    }
    if let e = assetErr { fail("embedding assets unavailable: \(e.localizedDescription)", code: 3) }
}
do { try embedding.load() } catch { fail("load embedding model: \(error.localizedDescription)", code: 4) }

let input = FileHandle.standardInput.readDataToEndOfFile()
let req: Request
do { req = try JSONDecoder().decode(Request.self, from: input) } catch {
    fail("decode request: \(error.localizedDescription)", code: 5)
}

var vectors: [[Float]] = []
vectors.reserveCapacity(req.texts.count)
for text in req.texts {
    if text.isEmpty {
        vectors.append([Float](repeating: 0, count: embedding.dimension))
        continue
    }
    do {
        let result = try embedding.embeddingResult(for: text, language: .english)
        var sum = [Double](repeating: 0, count: embedding.dimension)
        var count = 0
        result.enumerateTokenVectors(in: text.startIndex..<text.endIndex) { vector, _ in
            for (i, v) in vector.enumerated() { sum[i] += v }
            count += 1
            return true
        }
        if count == 0 {
            vectors.append([Float](repeating: 0, count: embedding.dimension))
        } else {
            vectors.append(sum.map { Float($0 / Double(count)) })
        }
    } catch { fail("embed: \(error.localizedDescription)", code: 6) }
}

let resp = Response(model: modelID, dim: embedding.dimension, vectors: vectors)
guard let out = try? JSONEncoder().encode(resp) else { fail("encode response", code: 7) }
FileHandle.standardOutput.write(out)
