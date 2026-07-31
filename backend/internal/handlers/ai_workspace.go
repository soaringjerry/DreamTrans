package handlers

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"math"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	aicontext "github.com/dreamtrans/backend/internal/ai"
	"github.com/dreamtrans/backend/internal/auth"
	"github.com/dreamtrans/backend/internal/models"
	"github.com/dreamtrans/backend/internal/store"
	"github.com/google/uuid"
)

const (
	defaultKnowledgeFileBytes = int64(50 << 20)
	knowledgeVectorDimensions = 384
)

func (h *RAGHandler) resumeKnowledgeIndexing() {
	if h.store == nil {
		return
	}
	sources, err := h.store.ListPendingKnowledgeSources(context.Background(), 100)
	if err != nil {
		return
	}
	for index := range sources {
		source := sources[index]
		go h.indexKnowledgeFile(
			context.Background(),
			&source,
			strings.ToLower(filepath.Ext(source.BlobPath)),
		)
	}
}

func (h *RAGHandler) HandleProjects(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		http.Error(w, "projects require PostgreSQL", http.StatusServiceUnavailable)
		return
	}
	claims := auth.GetUserClaims(r.Context())
	if claims == nil {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/ai/projects"), "/")
	if path == "" {
		h.handleProjectCollection(w, r, claims)
		return
	}
	parts := strings.Split(path, "/")
	projectID := parts[0]
	project, err := h.store.GetAIProject(r.Context(), projectID, claims.UserID)
	if err != nil {
		http.Error(w, "failed to load project", http.StatusInternalServerError)
		return
	}
	if project == nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}
	if len(parts) == 1 {
		h.handleProjectItem(w, r, project)
		return
	}
	switch parts[1] {
	case "sessions":
		h.handleProjectSession(w, r, project)
	case "sources":
		if len(parts) == 3 {
			h.handleKnowledgeSourceItem(w, r, project, parts[2])
		} else {
			h.handleKnowledgeSources(w, r, project)
		}
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (h *RAGHandler) handleProjectCollection(
	w http.ResponseWriter, r *http.Request, claims *auth.UserClaims,
) {
	switch r.Method {
	case http.MethodGet:
		projects, err := h.store.ListAIProjects(r.Context(), claims.UserID)
		if err != nil {
			http.Error(w, "failed to list projects", http.StatusInternalServerError)
			return
		}
		WriteJSON(w, map[string]any{"projects": projects})
	case http.MethodPost:
		var project models.AIProject
		if err := json.NewDecoder(r.Body).Decode(&project); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if project.ContextMode == "" {
			project.ContextMode = "smart"
		}
		if project.MaxContextTokens == 0 {
			project.MaxContextTokens = aicontext.DefaultContextTokens
		}
		project.UserID = claims.UserID
		project.TenantID = claims.TenantID
		if err := store.ValidateAIProject(&project); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := h.store.CreateAIProject(r.Context(), &project); err != nil {
			http.Error(w, "failed to create project", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		WriteJSON(w, map[string]any{"project": project})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *RAGHandler) handleProjectItem(
	w http.ResponseWriter, r *http.Request, project *models.AIProject,
) {
	switch r.Method {
	case http.MethodGet:
		WriteJSON(w, map[string]any{"project": project})
	case http.MethodPatch, http.MethodPut:
		var update struct {
			Name             *string `json:"name"`
			Description      *string `json:"description"`
			ContextMode      *string `json:"context_mode"`
			MaxContextTokens *int    `json:"max_context_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if update.Name != nil {
			project.Name = *update.Name
		}
		if update.Description != nil {
			project.Description = *update.Description
		}
		if update.ContextMode != nil {
			project.ContextMode = *update.ContextMode
		}
		if update.MaxContextTokens != nil {
			project.MaxContextTokens = *update.MaxContextTokens
		}
		if err := store.ValidateAIProject(project); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := h.store.UpdateAIProject(r.Context(), project); err != nil {
			http.Error(w, "failed to update project", http.StatusInternalServerError)
			return
		}
		WriteJSON(w, map[string]any{"project": project})
	case http.MethodDelete:
		sources, _ := h.store.ListKnowledgeSources(r.Context(), project.ID, project.UserID)
		if err := h.store.DeleteAIProject(r.Context(), project.ID, project.UserID); err != nil {
			http.Error(w, "failed to delete project", http.StatusInternalServerError)
			return
		}
		for _, source := range sources {
			_ = removeKnowledgeBlob(source.BlobPath)
		}
		WriteJSON(w, map[string]bool{"success": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *RAGHandler) handleKnowledgeSourceItem(
	w http.ResponseWriter,
	r *http.Request,
	project *models.AIProject,
	sourceID string,
) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	blobPath, err := h.store.DeleteKnowledgeSource(
		r.Context(), sourceID, project.ID, project.UserID,
	)
	if err != nil {
		http.Error(w, "knowledge source not found", http.StatusNotFound)
		return
	}
	if err := removeKnowledgeBlob(blobPath); err != nil {
		log.Printf("remove knowledge blob after metadata deletion: %v", err)
	}
	WriteJSON(w, map[string]bool{"success": true})
}

func (h *RAGHandler) handleProjectSession(
	w http.ResponseWriter, r *http.Request, project *models.AIProject,
) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if err := h.store.LinkProjectSession(
		r.Context(), project.ID, strings.TrimSpace(req.SessionID), project.UserID,
	); err != nil {
		http.Error(w, "session was not found or is not owned by this user", http.StatusNotFound)
		return
	}
	WriteJSON(w, map[string]bool{"success": true})
}

func (h *RAGHandler) handleKnowledgeSources(
	w http.ResponseWriter, r *http.Request, project *models.AIProject,
) {
	if r.Method == http.MethodGet {
		sources, err := h.store.ListKnowledgeSources(r.Context(), project.ID, project.UserID)
		if err != nil {
			http.Error(w, "failed to list knowledge sources", http.StatusInternalServerError)
			return
		}
		WriteJSON(w, map[string]any{"sources": sources})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	mediaType := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.HasPrefix(mediaType, "multipart/form-data") {
		h.handleKnowledgeFileUpload(w, r, project)
		return
	}
	var req struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Content = strings.TrimSpace(req.Content)
	if req.Name == "" || req.Content == "" || len([]rune(req.Content)) > 1_000_000 {
		http.Error(w, "memory name/content is required and content must be at most 1000000 characters", http.StatusBadRequest)
		return
	}
	source := &models.KnowledgeSource{
		ProjectID: project.ID, TenantID: project.TenantID, UserID: project.UserID,
		SourceType: "memory", Name: req.Name, MediaType: "text/plain",
		SizeBytes: int64(len(req.Content)), Status: "processing",
	}
	if err := h.store.CreateKnowledgeSource(r.Context(), source); err != nil {
		if errors.Is(err, store.ErrStorageQuota) {
			http.Error(w, "tenant storage quota exceeded", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "failed to create memory", http.StatusInternalServerError)
		return
	}
	chunks := makeKnowledgeChunks(source, req.Content)
	if err := h.store.ReplaceKnowledgeChunks(r.Context(), source, chunks); err != nil {
		_ = h.store.UpdateKnowledgeSourceStatus(
			r.Context(), source.ID, source.UserID, "error", err.Error(), 0,
		)
		http.Error(w, "failed to index memory", http.StatusInternalServerError)
		return
	}
	source.Status = "ready"
	source.ChunkCount = len(chunks)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	WriteJSON(w, map[string]any{"source": source})
}

func (h *RAGHandler) handleKnowledgeFileUpload(
	w http.ResponseWriter, r *http.Request, project *models.AIProject,
) {
	maxBytes := knowledgeFileLimit()
	if err := r.ParseMultipartForm(maxBytes + (1 << 20)); err != nil {
		http.Error(w, "file upload is too large or malformed", http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "file field is required", http.StatusBadRequest)
		return
	}
	defer func() { _ = file.Close() }()
	extension := strings.ToLower(filepath.Ext(header.Filename))
	if !supportedKnowledgeExtension(extension) {
		http.Error(w, "unsupported file type", http.StatusUnsupportedMediaType)
		return
	}
	root := os.Getenv("KNOWLEDGE_DATA_PATH")
	if strings.TrimSpace(root) == "" {
		root = "/app/data/knowledge"
	}
	root, err = filepath.Abs(root)
	if err != nil {
		http.Error(w, "invalid knowledge storage path", http.StatusInternalServerError)
		return
	}
	projectDir := filepath.Join(root, filepath.Base(project.ID))
	if err := os.MkdirAll(projectDir, 0o750); err != nil {
		http.Error(w, "failed to create knowledge storage", http.StatusInternalServerError)
		return
	}
	blobPath := filepath.Join(projectDir, uuid.NewString()+extension)
	destination, err := os.OpenFile(blobPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		http.Error(w, "failed to store file", http.StatusInternalServerError)
		return
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(destination, hasher), io.LimitReader(file, maxBytes+1))
	closeErr := destination.Close()
	if copyErr != nil || closeErr != nil || written > maxBytes {
		_ = os.Remove(blobPath)
		http.Error(w, "failed to store file or file exceeds limit", http.StatusRequestEntityTooLarge)
		return
	}
	source := &models.KnowledgeSource{
		ProjectID: project.ID, TenantID: project.TenantID, UserID: project.UserID,
		SourceType: "file", Name: safeKnowledgeFilename(header),
		MediaType: header.Header.Get("Content-Type"), SizeBytes: written,
		SHA256: hex.EncodeToString(hasher.Sum(nil)), BlobPath: blobPath, Status: "queued",
	}
	if source.MediaType == "" {
		source.MediaType = "application/octet-stream"
	}
	if err := h.store.CreateKnowledgeSource(r.Context(), source); err != nil {
		_ = os.Remove(blobPath)
		if errors.Is(err, store.ErrStorageQuota) {
			http.Error(w, "tenant storage quota exceeded", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "failed to register file", http.StatusInternalServerError)
		return
	}
	// Keep the durable queued row as the source of truth while the bounded
	// background job parses and indexes the file.
	background := context.WithoutCancel(r.Context())
	go h.indexKnowledgeFile(background, source, extension)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	WriteJSON(w, map[string]any{"source": source})
}

func (h *RAGHandler) indexKnowledgeFile(
	parent context.Context, source *models.KnowledgeSource, extension string,
) {
	ctx, cancel := context.WithTimeout(parent, 20*time.Minute)
	defer cancel()
	_ = h.store.UpdateKnowledgeSourceStatus(ctx, source.ID, source.UserID, "processing", "", 0)
	text, err := extractKnowledgeText(ctx, source.BlobPath, extension)
	if err == nil && strings.TrimSpace(text) == "" {
		err = errors.New("no readable text was found")
	}
	if err != nil {
		_ = h.store.UpdateKnowledgeSourceStatus(
			context.WithoutCancel(ctx), source.ID, source.UserID, "error", safeIndexError(err), 0,
		)
		return
	}
	chunks := makeKnowledgeChunks(source, text)
	if err := h.store.ReplaceKnowledgeChunks(ctx, source, chunks); err != nil {
		_ = h.store.UpdateKnowledgeSourceStatus(
			context.WithoutCancel(ctx), source.ID, source.UserID, "error", safeIndexError(err), 0,
		)
	}
}

func knowledgeFileLimit() int64 {
	raw := strings.TrimSpace(os.Getenv("KNOWLEDGE_MAX_FILE_MB"))
	if raw == "" {
		return defaultKnowledgeFileBytes
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 1 || value > 500 {
		return defaultKnowledgeFileBytes
	}
	return value << 20
}

func supportedKnowledgeExtension(extension string) bool {
	switch extension {
	case ".txt", ".md", ".csv", ".tsv", ".json", ".pdf", ".docx", ".xlsx",
		".png", ".jpg", ".jpeg", ".webp":
		return true
	default:
		return false
	}
}

func safeKnowledgeFilename(header *multipart.FileHeader) string {
	name := strings.TrimSpace(filepath.Base(header.Filename))
	if name == "" {
		return "uploaded-file"
	}
	if len([]rune(name)) > 255 {
		name = string([]rune(name)[:255])
	}
	return name
}

func removeKnowledgeBlob(blobPath string) error {
	blobPath = strings.TrimSpace(blobPath)
	if blobPath == "" {
		return nil
	}
	root := strings.TrimSpace(os.Getenv("KNOWLEDGE_DATA_PATH"))
	if root == "" {
		root = "/app/data/knowledge"
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	absoluteBlob, err := filepath.Abs(blobPath)
	if err != nil {
		return err
	}
	prefix := absoluteRoot + string(os.PathSeparator)
	if !strings.HasPrefix(absoluteBlob, prefix) {
		return errors.New("knowledge blob is outside the configured storage root")
	}
	if err := os.Remove(absoluteBlob); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func extractKnowledgeText(ctx context.Context, path, extension string) (string, error) {
	switch extension {
	case ".txt", ".md", ".json":
		data, err := os.ReadFile(path) //nolint:gosec // path is generated under the configured data root.
		return string(data), err
	case ".csv", ".tsv":
		return extractDelimited(path, extension == ".tsv")
	case ".docx":
		return extractOfficeXML(path, "word/document.xml")
	case ".xlsx":
		return extractXLSX(path)
	case ".pdf":
		return commandText(ctx, "pdftotext", "-layout", path, "-")
	case ".png", ".jpg", ".jpeg", ".webp":
		return commandText(ctx, "tesseract", path, "stdout")
	default:
		return "", errors.New("unsupported file type")
	}
}

func extractDelimited(path string, tabSeparated bool) (string, error) {
	file, err := os.Open(path) //nolint:gosec // generated knowledge path.
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	reader := csv.NewReader(bufio.NewReader(file))
	reader.FieldsPerRecord = -1
	if tabSeparated {
		reader.Comma = '\t'
	}
	var builder strings.Builder
	for {
		record, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", readErr
		}
		builder.WriteString(strings.Join(record, " | "))
		builder.WriteByte('\n')
	}
	return builder.String(), nil
}

func extractOfficeXML(path, member string) (string, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = reader.Close() }()
	for _, file := range reader.File {
		if file.Name != member {
			continue
		}
		stream, err := file.Open()
		if err != nil {
			return "", err
		}
		defer func() { _ = stream.Close() }()
		return extractXMLText(io.LimitReader(stream, 100<<20))
	}
	return "", fmt.Errorf("%s was not found in document", member)
}

func extractXLSX(path string) (string, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = reader.Close() }()
	sharedStrings := make([]string, 0)
	for _, file := range reader.File {
		if file.Name != "xl/sharedStrings.xml" {
			continue
		}
		stream, openErr := file.Open()
		if openErr != nil {
			return "", openErr
		}
		sharedStrings, err = extractSharedStrings(io.LimitReader(stream, 100<<20))
		_ = stream.Close()
		if err != nil {
			return "", err
		}
		break
	}
	var builder strings.Builder
	for _, file := range reader.File {
		if !strings.HasPrefix(file.Name, "xl/worksheets/") ||
			!strings.HasSuffix(file.Name, ".xml") {
			continue
		}
		stream, openErr := file.Open()
		if openErr != nil {
			return "", openErr
		}
		text, parseErr := extractWorksheetText(
			io.LimitReader(stream, 100<<20),
			sharedStrings,
		)
		_ = stream.Close()
		if parseErr != nil {
			return "", parseErr
		}
		builder.WriteString("[" + filepath.Base(file.Name) + "]\n")
		builder.WriteString(text)
		builder.WriteByte('\n')
	}
	return builder.String(), nil
}

func extractSharedStrings(reader io.Reader) ([]string, error) {
	decoder := xml.NewDecoder(reader)
	values := make([]string, 0)
	var current strings.Builder
	inItem := false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch value := token.(type) {
		case xml.StartElement:
			if value.Name.Local == "si" {
				current.Reset()
				inItem = true
			}
			if value.Name.Local == "t" && inItem {
				var text string
				if err := decoder.DecodeElement(&text, &value); err != nil {
					return nil, err
				}
				current.WriteString(text)
			}
		case xml.EndElement:
			if value.Name.Local == "si" && inItem {
				values = append(values, strings.TrimSpace(current.String()))
				inItem = false
			}
		}
	}
	return values, nil
}

func extractWorksheetText(reader io.Reader, sharedStrings []string) (string, error) {
	decoder := xml.NewDecoder(reader)
	var builder strings.Builder
	cellType := ""
	wroteCell := false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		switch value := token.(type) {
		case xml.StartElement:
			switch value.Name.Local {
			case "c":
				cellType = ""
				for _, attribute := range value.Attr {
					if attribute.Name.Local == "t" {
						cellType = attribute.Value
					}
				}
			case "v", "t":
				var text string
				if err := decoder.DecodeElement(&text, &value); err != nil {
					return "", err
				}
				if value.Name.Local == "v" && cellType == "s" {
					index, parseErr := strconv.Atoi(strings.TrimSpace(text))
					if parseErr == nil && index >= 0 && index < len(sharedStrings) {
						text = sharedStrings[index]
					}
				}
				text = strings.TrimSpace(text)
				if text != "" {
					if wroteCell {
						builder.WriteString(" | ")
					}
					builder.WriteString(text)
					wroteCell = true
				}
			}
		case xml.EndElement:
			if value.Name.Local == "row" {
				builder.WriteByte('\n')
				wroteCell = false
			}
		}
	}
	return builder.String(), nil
}

func extractXMLText(reader io.Reader) (string, error) {
	decoder := xml.NewDecoder(reader)
	var builder strings.Builder
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "t", "v":
			var value string
			if err := decoder.DecodeElement(&value, &start); err != nil {
				return "", err
			}
			value = strings.TrimSpace(value)
			if value != "" {
				builder.WriteString(value)
				builder.WriteByte(' ')
			}
		}
	}
	return builder.String(), nil
}

func commandText(ctx context.Context, command string, args ...string) (string, error) {
	if _, err := exec.LookPath(command); err != nil {
		return "", fmt.Errorf("%s is not installed on the server", command)
	}
	output, err := exec.CommandContext(ctx, command, args...).Output() //nolint:gosec // fixed executable and generated file path.
	if err != nil {
		return "", fmt.Errorf("%s extraction failed", command)
	}
	return string(output), nil
}

func makeKnowledgeChunks(
	source *models.KnowledgeSource, text string,
) []models.KnowledgeChunk {
	paragraphs := strings.FieldsFunc(strings.ReplaceAll(text, "\r\n", "\n"), func(r rune) bool {
		return r == '\n'
	})
	const maxRunes = 1_400
	chunks := make([]models.KnowledgeChunk, 0)
	var current strings.Builder
	flush := func() {
		content := strings.TrimSpace(current.String())
		if content == "" {
			current.Reset()
			return
		}
		chunks = append(chunks, models.KnowledgeChunk{
			SourceID: source.ID, ProjectID: source.ProjectID,
			Ordinal: len(chunks), Content: content,
			Vector: localKnowledgeVector(content),
		})
		current.Reset()
	}
	for _, paragraph := range paragraphs {
		paragraph = strings.Join(strings.Fields(paragraph), " ")
		if paragraph == "" {
			continue
		}
		runes := []rune(paragraph)
		for len(runes) > maxRunes {
			if current.Len() > 0 {
				flush()
			}
			piece := strings.TrimSpace(string(runes[:maxRunes]))
			chunks = append(chunks, models.KnowledgeChunk{
				SourceID: source.ID, ProjectID: source.ProjectID,
				Ordinal: len(chunks), Content: piece,
				Vector: localKnowledgeVector(piece),
			})
			runes = runes[maxRunes-160:]
		}
		paragraph = string(runes)
		if len([]rune(current.String()))+len([]rune(paragraph))+1 > maxRunes {
			flush()
		}
		if current.Len() > 0 {
			current.WriteByte('\n')
		}
		current.WriteString(paragraph)
	}
	flush()
	return chunks
}

func localKnowledgeVector(text string) []float64 {
	vector := make([]float64, knowledgeVectorDimensions)
	runes := []rune(strings.ToLower(text))
	add := func(term string, weight float64) {
		hash := fnv.New64a()
		_, _ = hash.Write([]byte(term))
		value := hash.Sum64()
		index := value % uint64(len(vector))
		sign := 1.0
		if value&(1<<63) != 0 {
			sign = -1
		}
		vector[index] += sign * weight
	}
	for _, word := range strings.FieldsFunc(string(runes), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}) {
		if word != "" {
			add("w:"+word, 2)
		}
	}
	for size := 2; size <= 4; size++ {
		for index := 0; index+size <= len(runes); index++ {
			gram := runes[index : index+size]
			if unicode.IsSpace(gram[0]) {
				continue
			}
			add("g:"+string(gram), 1)
		}
	}
	var norm float64
	for _, value := range vector {
		norm += value * value
	}
	if norm > 0 {
		norm = math.Sqrt(norm)
		for index := range vector {
			vector[index] /= norm
		}
	}
	return vector
}

func retrieveKnowledge(
	query string, chunks []models.KnowledgeChunk, topK int,
) []models.KnowledgeChunk {
	if topK <= 0 {
		topK = 6
	}
	queryVector := localKnowledgeVector(query)
	type scored struct {
		chunk models.KnowledgeChunk
		score float64
	}
	values := make([]scored, 0, len(chunks))
	for _, chunk := range chunks {
		var score float64
		for index := 0; index < len(queryVector) && index < len(chunk.Vector); index++ {
			score += queryVector[index] * chunk.Vector[index]
		}
		values = append(values, scored{chunk: chunk, score: score})
	}
	sort.Slice(values, func(i, j int) bool { return values[i].score > values[j].score })
	if len(values) > topK {
		values = values[:topK]
	}
	result := make([]models.KnowledgeChunk, 0, len(values))
	for _, value := range values {
		if value.score <= 0 && len(result) > 0 {
			continue
		}
		result = append(result, value.chunk)
	}
	return result
}

func safeIndexError(err error) string {
	message := strings.TrimSpace(err.Error())
	if len([]rune(message)) > 1_000 {
		message = string([]rune(message)[:1_000])
	}
	return message
}
