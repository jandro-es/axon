// axon-apple-ocr — AXON's Apple on-device OCR helper.
// Compiled by `axon init` from source embedded in the axon binary.
// Usage: axon-apple-ocr <pdf-path>   → stdout {"pages":["...", ...]}
//        axon-apple-ocr --check      → verify Vision is usable, exit 0
// Errors: message on stderr + non-zero exit. On-device (Vision); no network.
import Foundation
import PDFKit
import Vision
import CoreGraphics

struct Response: Encodable { let pages: [String] }

func fail(_ msg: String, code: Int32) -> Never {
    FileHandle.standardError.write((msg + "\n").data(using: .utf8)!)
    exit(code)
}

let args = CommandLine.arguments
if args.contains("--check") {
    _ = VNRecognizeTextRequest()
    print("vision ok")
    exit(0)
}
guard args.count >= 2 else { fail("usage: axon-apple-ocr <pdf-path>", code: 2) }
guard let doc = PDFDocument(url: URL(fileURLWithPath: args[1])) else {
    fail("cannot open PDF at \(args[1])", code: 3)
}

func recognize(_ cg: CGImage) -> String {
    let request = VNRecognizeTextRequest()
    request.recognitionLevel = .accurate
    request.usesLanguageCorrection = true
    let handler = VNImageRequestHandler(cgImage: cg, options: [:])
    do { try handler.perform([request]) } catch { return "" }
    guard let obs = request.results else { return "" }
    return obs.compactMap { $0.topCandidates(1).first?.string }.joined(separator: "\n")
}

var pages: [String] = []
let scale: CGFloat = 200.0 / 72.0 // ~200 dpi
for i in 0..<doc.pageCount {
    guard let page = doc.page(at: i) else { pages.append(""); continue }
    let bounds = page.bounds(for: .mediaBox)
    let w = Int(bounds.width * scale), h = Int(bounds.height * scale)
    guard w > 0, h > 0,
          let ctx = CGContext(data: nil, width: w, height: h, bitsPerComponent: 8,
                              bytesPerRow: 0, space: CGColorSpaceCreateDeviceRGB(),
                              bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else {
        pages.append("")
        continue
    }
    ctx.setFillColor(CGColor(red: 1, green: 1, blue: 1, alpha: 1))
    ctx.fill(CGRect(x: 0, y: 0, width: w, height: h))
    ctx.scaleBy(x: scale, y: scale)
    page.draw(with: .mediaBox, to: ctx)
    guard let cg = ctx.makeImage() else { pages.append(""); continue }
    pages.append(recognize(cg))
}

let data = try JSONEncoder().encode(Response(pages: pages))
FileHandle.standardOutput.write(data)
