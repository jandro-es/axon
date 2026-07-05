package core

import (
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

func TestOCRCheckReportsTesseractNeed(t *testing.T) {
	p := config.Profile{Ingestion: config.IngestionConfig{OCR: "tesseract"}}
	c := ocrCheck(p)
	// On a machine without the binaries this warns; either way it names OCR.
	if c.Name == "" || !strings.Contains(strings.ToLower(c.Name), "ocr") {
		t.Fatalf("ocrCheck name = %q", c.Name)
	}
}

func TestOCRCheckAppleMissingHelperWarns(t *testing.T) {
	p := config.Profile{Ingestion: config.IngestionConfig{OCR: "apple", OCRHelper: "/no/such/helper"}}
	c := ocrCheck(p)
	if c.Status != StatusWarn {
		t.Fatalf("apple missing helper should warn, got %v", c.Status)
	}
}
