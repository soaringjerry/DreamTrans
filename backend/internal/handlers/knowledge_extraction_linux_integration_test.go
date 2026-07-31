//go:build linux && knowledge_extraction_integration

package handlers

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestLinuxKnowledgeExtractionFixtures(t *testing.T) {
	for _, command := range []string{"pdftotext", "pdfinfo", "pdftoppm", "tesseract"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Fatalf("production dependency %s is unavailable: %v", command, err)
		}
	}

	t.Setenv("KNOWLEDGE_MAX_EXTRACTED_MB", "2")
	t.Setenv("KNOWLEDGE_MAX_OFFICE_UNCOMPRESSED_MB", "1")
	t.Setenv("KNOWLEDGE_MAX_IMAGE_MEGAPIXELS", "20")
	t.Setenv("KNOWLEDGE_MAX_PDF_PAGES", "1")

	ctx := context.Background()
	root := t.TempDir()

	textPDF := filepath.Join(root, "text.pdf")
	writeTextFixturePDF(t, textPDF, "DREAMTRANS TEXT PDF 42")
	assertKnowledgeMediaType(t, textPDF, ".pdf", "application/pdf")
	text, err := extractKnowledgeText(ctx, textPDF, ".pdf", []string{"eng"})
	if err != nil {
		t.Fatalf("extract text PDF: %v", err)
	}
	assertExtractedContains(t, "text PDF", text, "DREAMTRANS TEXT PDF 42")

	imageRoot := filepath.Join(root, "ocr-source")
	if _, err := runBoundedKnowledgeCommand(
		ctx,
		knowledgeCommandErrorBytes,
		"pdftoppm",
		"-f",
		"1",
		"-l",
		"1",
		"-png",
		"-singlefile",
		"-scale-to",
		"1800",
		textPDF,
		imageRoot,
	); err != nil {
		t.Fatalf("render OCR image fixture: %v", err)
	}
	imagePath := imageRoot + ".png"
	assertKnowledgeMediaType(t, imagePath, ".png", "image/png")
	imageText, err := extractKnowledgeText(ctx, imagePath, ".png", []string{"eng"})
	if err != nil {
		t.Fatalf("extract image: %v", err)
	}
	assertExtractedNotEmpty(t, "image", imageText)

	for _, language := range []string{"eng", "chi_sim", "jpn", "kor"} {
		if _, err := runBoundedKnowledgeCommand(
			ctx,
			1<<20,
			"tesseract",
			imagePath,
			"stdout",
			"-l",
			language,
		); err != nil {
			t.Fatalf("load and run OCR language %s: %v", language, err)
		}
	}

	scannedPDF := filepath.Join(root, "scanned.pdf")
	writeRasterFixturePDF(t, scannedPDF, imagePath, 1)
	assertKnowledgeMediaType(t, scannedPDF, ".pdf", "application/pdf")
	scannedText, err := extractKnowledgeText(
		ctx,
		scannedPDF,
		".pdf",
		[]string{"eng"},
	)
	if err != nil {
		t.Fatalf("extract scanned PDF through OCR fallback: %v", err)
	}
	assertExtractedNotEmpty(t, "scanned PDF", scannedText)

	oversizedPDF := filepath.Join(root, "too-many-scanned-pages.pdf")
	writeRasterFixturePDF(t, oversizedPDF, imagePath, 2)
	if _, err := extractKnowledgeText(
		ctx,
		oversizedPDF,
		".pdf",
		[]string{"eng"},
	); err == nil || !strings.Contains(err.Error(), "limit is 1") {
		t.Fatalf("scanned PDF page limit error = %v", err)
	}

	docxPath := filepath.Join(root, "fixture.docx")
	writeZIPFixture(t, docxPath, map[string][]byte{
		"[Content_Types].xml": []byte(
			`<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`,
		),
		"word/document.xml": []byte(
			`<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>DREAMTRANS DOCX 42</w:t></w:r></w:p></w:body></w:document>`,
		),
	})
	assertKnowledgeMediaType(
		t,
		docxPath,
		".docx",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	)
	docxText, err := extractKnowledgeText(ctx, docxPath, ".docx", nil)
	if err != nil {
		t.Fatalf("extract DOCX: %v", err)
	}
	assertExtractedContains(t, "DOCX", docxText, "DREAMTRANS DOCX 42")

	xlsxPath := filepath.Join(root, "fixture.xlsx")
	writeZIPFixture(t, xlsxPath, map[string][]byte{
		"[Content_Types].xml": []byte(
			`<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`,
		),
		"xl/workbook.xml": []byte(
			`<?xml version="1.0"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"/>`,
		),
		"xl/sharedStrings.xml": []byte(
			`<?xml version="1.0"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><si><t>DREAMTRANS XLSX</t></si></sst>`,
		),
		"xl/worksheets/sheet1.xml": []byte(
			`<?xml version="1.0"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row><c t="s"><v>0</v></c><c t="inlineStr"><is><t>42</t></is></c></row></sheetData></worksheet>`,
		),
	})
	assertKnowledgeMediaType(
		t,
		xlsxPath,
		".xlsx",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	)
	xlsxText, err := extractKnowledgeText(ctx, xlsxPath, ".xlsx", nil)
	if err != nil {
		t.Fatalf("extract XLSX: %v", err)
	}
	assertExtractedContains(t, "XLSX", xlsxText, "DREAMTRANS XLSX", "42")

	zipBombPath := filepath.Join(root, "zip-bomb.docx")
	writeZIPFixture(t, zipBombPath, map[string][]byte{
		"[Content_Types].xml": []byte("<Types/>"),
		"word/document.xml":   []byte("<document/>"),
		"../escape.txt":       bytes.Repeat([]byte("A"), (1<<20)+1),
	})
	outsidePath := filepath.Join(filepath.Dir(root), "escape.txt")
	if _, err := validateKnowledgeUpload(
		zipBombPath,
		".docx",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	); !errors.Is(err, errKnowledgeOfficeTooLarge) {
		t.Fatalf("oversized malicious archive error = %v", err)
	}
	if _, err := os.Stat(outsidePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("malicious archive wrote outside its fixture directory: %v", err)
	}
}

func assertKnowledgeMediaType(
	t *testing.T,
	path string,
	extension string,
	declared string,
) {
	t.Helper()
	got, err := validateKnowledgeUpload(path, extension, declared)
	if err != nil {
		t.Fatalf("validate %s fixture: %v", extension, err)
	}
	if got != declared {
		t.Fatalf("validate %s media type = %q, want %q", extension, got, declared)
	}
}

func assertExtractedContains(
	t *testing.T,
	fixtureName string,
	text string,
	fragments ...string,
) {
	t.Helper()
	normalized := strings.ToUpper(strings.Join(strings.Fields(text), " "))
	for _, fragment := range fragments {
		if !strings.Contains(normalized, strings.ToUpper(fragment)) {
			t.Fatalf("%s extracted text %q does not contain %q", fixtureName, text, fragment)
		}
	}
}

func assertExtractedNotEmpty(t *testing.T, fixtureName, text string) {
	t.Helper()
	if strings.TrimSpace(text) == "" {
		t.Fatalf("%s extraction returned no text", fixtureName)
	}
}

func writeZIPFixture(t *testing.T, path string, members map[string][]byte) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	names := make([]string, 0, len(members))
	for name := range members {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		content := members[name]
		entry, createErr := writer.Create(name)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, writeErr := entry.Write(content); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeTextFixturePDF(t *testing.T, path, text string) {
	t.Helper()
	text = strings.NewReplacer(
		`\`, `\\`,
		`(`, `\(`,
		`)`, `\)`,
	).Replace(text)
	content := []byte("BT\n/F1 28 Tf\n50 170 Td\n(" + text + ") Tj\nET\n")
	writeFixturePDF(t, path, [][]byte{
		[]byte("<< /Type /Catalog /Pages 2 0 R >>"),
		[]byte("<< /Type /Pages /Kids [3 0 R] /Count 1 >>"),
		[]byte(
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 640 240] " +
				"/Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
		),
		[]byte("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold >>"),
		pdfStreamObject("", content),
	})
}

func writeRasterFixturePDF(t *testing.T, path, imagePath string, pages int) {
	t.Helper()
	file, err := os.Open(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	source, _, err := image.Decode(file)
	closeErr := file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	raw := make([]byte, 0, width*height*3)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			red, green, blue, _ := source.At(x, y).RGBA()
			raw = append(raw, byte(red>>8), byte(green>>8), byte(blue>>8))
		}
	}
	var compressed bytes.Buffer
	compressor := zlib.NewWriter(&compressed)
	if _, err := compressor.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := compressor.Close(); err != nil {
		t.Fatal(err)
	}

	imageObjectID := 3 + pages
	contentObjectID := imageObjectID + 1
	kids := make([]string, 0, pages)
	objects := make([][]byte, 0, pages+4)
	objects = append(objects, []byte("<< /Type /Catalog /Pages 2 0 R >>"))
	for page := 0; page < pages; page++ {
		kids = append(kids, strconv.Itoa(3+page)+" 0 R")
	}
	objects = append(objects, []byte(fmt.Sprintf(
		"<< /Type /Pages /Kids [%s] /Count %d >>",
		strings.Join(kids, " "),
		pages,
	)))
	for page := 0; page < pages; page++ {
		objects = append(objects, []byte(fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %d %d] "+
				"/Resources << /XObject << /Im0 %d 0 R >> >> "+
				"/Contents %d 0 R >>",
			width,
			height,
			imageObjectID,
			contentObjectID,
		)))
	}
	objects = append(objects, pdfStreamObject(fmt.Sprintf(
		"/Type /XObject /Subtype /Image /Width %d /Height %d "+
			"/ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode",
		width,
		height,
	), compressed.Bytes()))
	objects = append(objects, pdfStreamObject("", []byte(fmt.Sprintf(
		"q\n%d 0 0 %d 0 0 cm\n/Im0 Do\nQ\n",
		width,
		height,
	))))
	writeFixturePDF(t, path, objects)
}

func pdfStreamObject(dictionary string, stream []byte) []byte {
	var object bytes.Buffer
	_, _ = fmt.Fprintf(&object, "<< %s /Length %d >>\nstream\n", dictionary, len(stream))
	_, _ = object.Write(stream)
	_, _ = object.WriteString("\nendstream")
	return object.Bytes()
}

func writeFixturePDF(t *testing.T, path string, objects [][]byte) {
	t.Helper()
	var document bytes.Buffer
	_, _ = document.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = document.Len()
		_, _ = fmt.Fprintf(&document, "%d 0 obj\n", index+1)
		_, _ = document.Write(object)
		_, _ = document.WriteString("\nendobj\n")
	}
	xrefOffset := document.Len()
	_, _ = fmt.Fprintf(&document, "xref\n0 %d\n", len(objects)+1)
	_, _ = document.WriteString("0000000000 65535 f \n")
	for _, offset := range offsets[1:] {
		_, _ = fmt.Fprintf(&document, "%010d 00000 n \n", offset)
	}
	_, _ = fmt.Fprintf(
		&document,
		"trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len(objects)+1,
		xrefOffset,
	)
	if err := os.WriteFile(path, document.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}
