package handlers

import (
	"archive/zip"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestParseKnowledgeOCRLanguages(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		got, err := parseKnowledgeOCRLanguages(nil)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"eng", "chi_sim"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("languages = %#v, want %#v", got, want)
		}
		if argument := tesseractLanguageArgument(got); argument != "eng+chi_sim" {
			t.Fatalf("tesseract language argument = %q", argument)
		}
	})

	t.Run("repeated and deduplicated", func(t *testing.T) {
		got, err := parseKnowledgeOCRLanguages(
			[]string{"jpn", " ENG ", "jpn", "kor"},
		)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"jpn", "eng", "kor"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("languages = %#v, want %#v", got, want)
		}
	})

	t.Run("reject unsupported", func(t *testing.T) {
		if _, err := parseKnowledgeOCRLanguages([]string{"fra"}); err == nil {
			t.Fatal("expected unsupported language error")
		}
	})
}

func TestDefaultKnowledgeOCRLanguagesFromSessionLanguage(t *testing.T) {
	tests := []struct {
		name     string
		language string
		want     []string
	}{
		{name: "english", language: "en-US", want: []string{"eng"}},
		{name: "mandarin", language: "cmn", want: []string{"chi_sim"}},
		{name: "Chinese BCP 47", language: "zh_CN", want: []string{"chi_sim"}},
		{name: "Japanese", language: "ja-JP", want: []string{"jpn"}},
		{name: "Korean", language: "kor", want: []string{"kor"}},
		{
			name: "unknown or no linked session",
			want: []string{"eng", "chi_sim"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := defaultKnowledgeOCRLanguages(test.language)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("languages = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestKnowledgeExtractionEnvironmentLimits(t *testing.T) {
	t.Setenv("KNOWLEDGE_MAX_EXTRACTED_MB", "7")
	t.Setenv("KNOWLEDGE_MAX_OFFICE_UNCOMPRESSED_MB", "23")
	t.Setenv("KNOWLEDGE_MAX_IMAGE_MEGAPIXELS", "12")
	t.Setenv("KNOWLEDGE_MAX_PDF_PAGES", "45")
	t.Setenv("KNOWLEDGE_EXTRACT_WORKERS", "5")
	limits := currentKnowledgeExtractionLimits()
	if limits.maxExtractedBytes != 7<<20 {
		t.Fatalf("max extracted bytes = %d", limits.maxExtractedBytes)
	}
	if limits.maxOfficeUncompressedBytes != 23<<20 {
		t.Fatalf("max office bytes = %d", limits.maxOfficeUncompressedBytes)
	}
	if limits.maxImagePixels != 12_000_000 {
		t.Fatalf("max image pixels = %d", limits.maxImagePixels)
	}
	if limits.maxPDFPages != 45 {
		t.Fatalf("max PDF pages = %d", limits.maxPDFPages)
	}
	if workers := knowledgeExtractWorkerCount(); workers != 5 {
		t.Fatalf("workers = %d", workers)
	}
}

func TestBoundedTextExtractionRejectsOverflow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.txt")
	if err := os.WriteFile(path, []byte("123456"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := readBoundedTextFile(path, 5)
	if !errors.Is(err, errKnowledgeExtractedTextTooLarge) {
		t.Fatalf("error = %v", err)
	}

	var commandOutput boundedCommandBuffer
	commandOutput.limit = 4
	if _, err := commandOutput.Write([]byte("abcdef")); err != nil {
		t.Fatal(err)
	}
	if !commandOutput.exceeded || commandOutput.buffer.String() != "abcd" {
		t.Fatalf(
			"bounded output = %q, exceeded=%v",
			commandOutput.buffer.String(),
			commandOutput.exceeded,
		)
	}
}

func TestStructuredExtractorsRejectTextOverflow(t *testing.T) {
	if _, err := extractXMLTextBounded(
		strings.NewReader(`<document><t>abcdef</t></document>`),
		5,
	); !errors.Is(err, errKnowledgeExtractedTextTooLarge) {
		t.Fatalf("XML error = %v", err)
	}

	path := filepath.Join(t.TempDir(), "large.csv")
	if err := os.WriteFile(path, []byte("first,second\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := extractDelimited(path, false, 5); !errors.Is(
		err,
		errKnowledgeExtractedTextTooLarge,
	) {
		t.Fatalf("CSV error = %v", err)
	}
}

func TestSpreadsheetStructureLimitsRejectEmptyItemAmplification(t *testing.T) {
	_, err := extractSharedStringsLimited(
		strings.NewReader(`<sst><si/><si/><si/></sst>`),
		1<<20,
		2,
	)
	if !errors.Is(err, errKnowledgeSpreadsheetTooComplex) {
		t.Fatalf("shared strings error = %v", err)
	}

	_, cellsRead, err := extractWorksheetTextLimited(
		strings.NewReader(
			`<worksheet><sheetData><row><c/><c/><c/></row></sheetData></worksheet>`,
		),
		nil,
		1<<20,
		2,
	)
	if !errors.Is(err, errKnowledgeSpreadsheetTooComplex) {
		t.Fatalf("worksheet error = %v", err)
	}
	if cellsRead != 3 {
		t.Fatalf("cells read = %d, want 3", cellsRead)
	}
}

func TestOfficeArchiveCumulativeDecompressedLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.docx")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for index := range 2 {
		member, createErr := writer.Create("part-" + strconv.Itoa(index))
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, writeErr := member.Write([]byte(strings.Repeat("x", 40))); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	reader, err := openBoundedOfficeArchive(path, 64)
	if reader != nil {
		_ = reader.Close()
	}
	if !errors.Is(err, errKnowledgeOfficeTooLarge) {
		t.Fatalf("error = %v", err)
	}
}

func TestOfficeArchiveEnforcesActualReadBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime-budget.docx")
	writeTestZIP(t, path, map[string]string{
		"word/document.xml": strings.Repeat("<w:t>x</w:t>", 100),
	})
	reader, err := openBoundedOfficeArchive(path, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	// Exercise the streaming guard independently of central-directory sizes.
	// In production this same shared budget protects every XML member.
	reader.limit = 64
	stream, err := reader.Open(reader.File[0])
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := io.ReadAll(stream)
	_ = stream.Close()
	if !errors.Is(readErr, errKnowledgeOfficeTooLarge) {
		t.Fatalf("runtime decompression error = %v", readErr)
	}
}

func TestValidateOfficeContainerRequiresExpectedMembers(t *testing.T) {
	root := t.TempDir()
	missingDocument := filepath.Join(root, "missing.docx")
	writeTestZIP(t, missingDocument, map[string]string{
		"[Content_Types].xml": "<Types/>",
	})
	limits := knowledgeExtractionLimits{maxOfficeUncompressedBytes: 1 << 20}
	if err := validateOfficeContainer(missingDocument, ".docx", limits); err == nil {
		t.Fatal("expected missing document member error")
	}

	validDocument := filepath.Join(root, "valid.docx")
	writeTestZIP(t, validDocument, map[string]string{
		"[Content_Types].xml": "<Types/>",
		"word/document.xml":   "<document/>",
	})
	if err := validateOfficeContainer(validDocument, ".docx", limits); err != nil {
		t.Fatal(err)
	}
	mediaType, err := validateKnowledgeUpload(
		validDocument,
		".docx",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	)
	if err != nil {
		t.Fatal(err)
	}
	if mediaType != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" {
		t.Fatalf("media type = %q", mediaType)
	}
}

func TestValidateKnowledgeUploadUsesMagicAndDeclaredType(t *testing.T) {
	root := t.TempDir()
	textPath := filepath.Join(root, "note.txt")
	if err := os.WriteFile(textPath, []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mediaType, err := validateKnowledgeUpload(textPath, ".txt", "text/plain; charset=utf-8")
	if err != nil {
		t.Fatal(err)
	}
	if mediaType != "text/plain" {
		t.Fatalf("media type = %q", mediaType)
	}

	fakePDF := filepath.Join(root, "fake.pdf")
	if err := os.WriteFile(fakePDF, []byte("not really a PDF"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := validateKnowledgeUpload(fakePDF, ".pdf", "application/pdf"); err == nil {
		t.Fatal("expected magic mismatch")
	}
	if _, err := validateKnowledgeUpload(textPath, ".txt", "image/png"); err == nil {
		t.Fatal("expected declared media type mismatch")
	}
}

func writeTestZIP(t *testing.T, path string, members map[string]string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, content := range members {
		member, createErr := writer.Create(name)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, writeErr := member.Write([]byte(content)); writeErr != nil {
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

func TestValidateKnowledgeImagePixelLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := validateKnowledgeImage(path, ".png", 63); !errors.Is(err, errKnowledgeImageTooLarge) {
		t.Fatalf("error = %v", err)
	}
	if err := validateKnowledgeImage(path, ".png", 64); err != nil {
		t.Fatal(err)
	}
}

func TestExtractPDFTextFallsBackToBoundedOCR(t *testing.T) {
	commands := make([]string, 0)
	runner := func(
		_ context.Context, _ int64, command string, args ...string,
	) ([]byte, error) {
		commands = append(commands, command)
		switch command {
		case "pdftotext":
			return []byte(" \n"), nil
		case "pdfinfo":
			return []byte("Title: Scan\nPages: 2\n"), nil
		case "pdftoppm":
			outputRoot := args[len(args)-1]
			file, err := os.Create(outputRoot + ".png")
			if err != nil {
				return nil, err
			}
			rendered := image.NewRGBA(image.Rect(0, 0, 1, 1))
			rendered.Set(0, 0, color.White)
			encodeErr := png.Encode(file, rendered)
			closeErr := file.Close()
			if encodeErr != nil {
				return nil, encodeErr
			}
			return nil, closeErr
		case "tesseract":
			if len(commands) == 4 {
				return []byte("first page"), nil
			}
			return []byte("second page"), nil
		default:
			t.Fatalf("unexpected command %q", command)
			return nil, nil
		}
	}
	text, err := extractPDFText(
		context.Background(),
		"/does/not/need/to/exist.pdf",
		[]string{"eng", "jpn"},
		knowledgeExtractionLimits{
			maxExtractedBytes: 1 << 20,
			maxImagePixels:    10,
			maxPDFPages:       3,
		},
		runner,
	)
	if err != nil {
		t.Fatal(err)
	}
	if text != "first page\nsecond page" {
		t.Fatalf("text = %q", text)
	}
	wantCommands := []string{
		"pdftotext", "pdfinfo", "pdftoppm", "tesseract", "pdftoppm", "tesseract",
	}
	if !reflect.DeepEqual(commands, wantCommands) {
		t.Fatalf("commands = %#v", commands)
	}
}

func TestExtractPDFTextSkipsOCRWhenTextExists(t *testing.T) {
	calls := 0
	runner := func(
		_ context.Context, _ int64, command string, _ ...string,
	) ([]byte, error) {
		calls++
		if command != "pdftotext" {
			t.Fatalf("unexpected fallback command %q", command)
		}
		return []byte("readable"), nil
	}
	text, err := extractPDFText(
		context.Background(),
		"document.pdf",
		nil,
		knowledgeExtractionLimits{maxExtractedBytes: 100, maxPDFPages: 1},
		runner,
	)
	if err != nil {
		t.Fatal(err)
	}
	if text != "readable" || calls != 1 {
		t.Fatalf("text=%q calls=%d", text, calls)
	}
}

func TestParsePDFPageCount(t *testing.T) {
	pages, err := parsePDFPageCount("Producer: test\nPAGES:   17\n")
	if err != nil {
		t.Fatal(err)
	}
	if pages != 17 {
		t.Fatalf("pages = %d", pages)
	}
	if _, err := parsePDFPageCount("Title: no page field"); err == nil {
		t.Fatal("expected missing page count error")
	}
}

func TestExtractPPTXReadsSlidesInOrderWithNotes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deck.pptx")
	writeTestZIP(t, path, map[string]string{
		"[Content_Types].xml":             "<Types/>",
		"ppt/presentation.xml":            "<presentation/>",
		"ppt/slides/slide10.xml":          `<p:sld><p:txBody><a:p><a:r><a:t>Tenth slide</a:t></a:r></a:p></p:txBody></p:sld>`,
		"ppt/slides/slide2.xml":           `<p:sld><a:t>Correlation</a:t><a:t> is not causation</a:t></p:sld>`,
		"ppt/notesSlides/notesSlide2.xml": `<p:notes><a:t>Remind them about confounds</a:t></p:notes>`,
	})
	limits := knowledgeExtractionLimits{
		maxOfficeUncompressedBytes: 1 << 20, maxExtractedBytes: 1 << 20,
	}
	if err := validateOfficeContainer(path, ".pptx", limits); err != nil {
		t.Fatal(err)
	}
	text, err := extractPPTX(path, limits)
	if err != nil {
		t.Fatal(err)
	}
	slide2 := strings.Index(text, "[Slide 2]")
	slide10 := strings.Index(text, "[Slide 10]")
	if slide2 < 0 || slide10 < 0 || slide2 > slide10 {
		t.Fatalf("slides must be numbered in deck order:\n%s", text)
	}
	for _, want := range []string{"Correlation", "is not causation", "[Notes]", "Remind them about confounds", "Tenth slide"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	mediaType, err := validateKnowledgeUpload(
		path, ".pptx",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation",
	)
	if err != nil || !strings.HasSuffix(mediaType, "presentation") {
		t.Fatalf("pptx upload validation: type=%q err=%v", mediaType, err)
	}
}
