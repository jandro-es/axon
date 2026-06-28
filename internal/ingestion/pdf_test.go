package ingestion

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
)

// makeTestPDF builds a minimal, valid single-page PDF containing one text run,
// computing the cross-reference table offsets so the file parses. This avoids a
// binary fixture and keeps the test self-contained.
func makeTestPDF(text string) []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, 6) // 1..5
	obj := func(n int, body string) {
		offsets[n] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", n, body)
	}
	content := fmt.Sprintf("BT /F1 24 Tf 72 700 Td (%s) Tj ET", text)
	obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>")
	obj(4, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	obj(5, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")

	xref := buf.Len()
	buf.WriteString("xref\n0 6\n")
	buf.WriteString("0000000000 65535 f \n")
	for n := 1; n <= 5; n++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[n])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	return buf.Bytes()
}

func TestExtractPDF(t *testing.T) {
	ex, err := ExtractPDF(makeTestPDF("Hello AXON PDF"), "doc.pdf")
	if err != nil {
		t.Fatalf("ExtractPDF: %v", err)
	}
	if !strings.Contains(ex.Markdown, "Hello AXON PDF") {
		t.Errorf("extracted text missing expected content: %q", ex.Markdown)
	}
}

func TestExtractPDFMalformedIsErrorNotPanic(t *testing.T) {
	// Garbage bytes must produce an error, never a panic (recover guard).
	if _, err := ExtractPDF([]byte("%PDF-1.4\nnot really a pdf"), "bad.pdf"); err == nil {
		t.Error("expected an error for malformed PDF")
	}
}

func TestIngestPDFEndToEnd(t *testing.T) {
	p, _, _ := newTestPipeline(t, openPolicy())
	dir := t.TempDir()
	ctx := context.Background()

	file := writeFileBytes(t, dir, "paper.pdf", makeTestPDF("Vector databases index embeddings"))
	res, err := p.Ingest(ctx, file, IngestOptions{AllowLocalFiles: true})
	if err != nil {
		t.Fatalf("ingest pdf: %v", err)
	}
	if res.Status != "ok" || res.NotePath == "" {
		t.Fatalf("unexpected pdf ingest result: %+v", res)
	}
	if !p.Vault.Exists(res.NotePath) {
		t.Errorf("pdf source note not written at %q", res.NotePath)
	}
}

func TestIngestPDFRefusedOnAgentPath(t *testing.T) {
	p, _, _ := newTestPipeline(t, openPolicy())
	dir := t.TempDir()
	file := writeFileBytes(t, dir, "x.pdf", makeTestPDF("secret"))
	// AllowLocalFiles defaults false (the MCP/agent path): local PDFs are refused.
	if _, err := p.Ingest(context.Background(), file, IngestOptions{}); err == nil {
		t.Error("agent-path PDF ingestion of a local file must be refused")
	}
}

func writeFileBytes(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	return writeFile(t, dir, name, string(data))
}
