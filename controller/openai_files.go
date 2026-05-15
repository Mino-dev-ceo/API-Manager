package controller

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

const (
	openAIFileObject = "file"
	openAIFilePrefix = "file-"
	openAIFileMaxMB  = 32
)

type openAIFileMeta struct {
	ID        string `json:"id"`
	Object    string `json:"object"`
	Bytes     int64  `json:"bytes"`
	CreatedAt int64  `json:"created_at"`
	Filename  string `json:"filename"`
	Purpose   string `json:"purpose"`
	MimeType  string `json:"mime_type,omitempty"`
	Path      string `json:"-"`
}

var openAIFileMu sync.Mutex

func openAIFileStoreDir() string {
	if dataDir := strings.TrimSpace(os.Getenv("DATA_DIR")); dataDir != "" {
		return filepath.Join(dataDir, "openai-files")
	}
	return filepath.Join(os.TempDir(), "new-api-openai-files")
}

func openAIFileIndexPath() string {
	return filepath.Join(openAIFileStoreDir(), "index.json")
}

func openAIFileIndex() map[string]openAIFileMeta {
	index := map[string]openAIFileMeta{}
	data, err := os.ReadFile(openAIFileIndexPath())
	if err == nil {
		_ = json.Unmarshal(data, &index)
	}
	return index
}

func saveOpenAIFileIndex(index map[string]openAIFileMeta) {
	_ = os.MkdirAll(openAIFileStoreDir(), 0o700)
	data, _ := json.MarshalIndent(index, "", "  ")
	_ = os.WriteFile(openAIFileIndexPath(), data, 0o600)
}

func sanitizeOpenAIFileName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "." || name == "/" || name == "" {
		return "upload.bin"
	}
	return name
}

func detectFileMime(header *multipart.FileHeader, path string) string {
	if header != nil {
		if ct := header.Header.Get("Content-Type"); ct != "" {
			return strings.Split(ct, ";")[0]
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return "application/octet-stream"
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	return http.DetectContentType(buf[:n])
}

func openAIFilePublic(meta openAIFileMeta) gin.H {
	return gin.H{
		"id":         meta.ID,
		"object":     openAIFileObject,
		"bytes":      meta.Bytes,
		"created_at": meta.CreatedAt,
		"filename":   meta.Filename,
		"purpose":    meta.Purpose,
	}
}

func UploadOpenAIFile(c *gin.Context) {
	if err := c.Request.ParseMultipartForm(openAIFileMaxMB << 20); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid multipart upload", "type": "invalid_request_error", "param": "file"}})
		return
	}
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Missing file field", "type": "invalid_request_error", "param": "file"}})
		return
	}
	defer file.Close()

	purpose := c.PostForm("purpose")
	if purpose == "" {
		purpose = "assistants"
	}

	storeDir := openAIFileStoreDir()
	if err := os.MkdirAll(storeDir, 0o700); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "File storage unavailable", "type": "server_error"}})
		return
	}

	id := openAIFilePrefix + common.GetUUID()[:24]
	filename := sanitizeOpenAIFileName(header.Filename)
	path := filepath.Join(storeDir, id+"-"+filename)
	dst, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "File storage unavailable", "type": "server_error"}})
		return
	}
	written, copyErr := io.Copy(dst, io.LimitReader(file, (openAIFileMaxMB<<20)+1))
	closeErr := dst.Close()
	if copyErr != nil || closeErr != nil || written > (openAIFileMaxMB<<20) {
		_ = os.Remove(path)
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("File exceeds %dMB limit", openAIFileMaxMB), "type": "invalid_request_error", "param": "file"}})
		return
	}

	meta := openAIFileMeta{
		ID:        id,
		Object:    openAIFileObject,
		Bytes:     written,
		CreatedAt: time.Now().Unix(),
		Filename:  filename,
		Purpose:   purpose,
		MimeType:  detectFileMime(header, path),
		Path:      path,
	}
	openAIFileMu.Lock()
	defer openAIFileMu.Unlock()
	index := openAIFileIndex()
	index[id] = meta
	saveOpenAIFileIndex(index)
	c.JSON(http.StatusOK, openAIFilePublic(meta))
}

func ListOpenAIFiles(c *gin.Context) {
	openAIFileMu.Lock()
	defer openAIFileMu.Unlock()
	index := openAIFileIndex()
	data := make([]gin.H, 0, len(index))
	for _, meta := range index {
		data = append(data, openAIFilePublic(meta))
	}
	c.JSON(http.StatusOK, gin.H{"object": "list", "data": data})
}

func GetOpenAIFile(c *gin.Context) {
	openAIFileMu.Lock()
	defer openAIFileMu.Unlock()
	meta, ok := openAIFileIndex()[c.Param("id")]
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "File not found", "type": "invalid_request_error", "param": "file_id"}})
		return
	}
	c.JSON(http.StatusOK, openAIFilePublic(meta))
}

func GetOpenAIFileContent(c *gin.Context) {
	openAIFileMu.Lock()
	meta, ok := openAIFileIndex()[c.Param("id")]
	openAIFileMu.Unlock()
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "File not found", "type": "invalid_request_error", "param": "file_id"}})
		return
	}
	c.File(meta.Path)
}

func DeleteOpenAIFile(c *gin.Context) {
	id := c.Param("id")
	openAIFileMu.Lock()
	defer openAIFileMu.Unlock()
	index := openAIFileIndex()
	meta, ok := index[id]
	if ok {
		_ = os.Remove(meta.Path)
		delete(index, id)
		saveOpenAIFileIndex(index)
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "object": "file", "deleted": ok})
}

func resolveOpenAIFileDataURL(fileID string) (string, bool) {
	openAIFileMu.Lock()
	meta, ok := openAIFileIndex()[fileID]
	openAIFileMu.Unlock()
	if !ok {
		return "", false
	}
	data, err := os.ReadFile(meta.Path)
	if err != nil {
		return "", false
	}
	mimeType := meta.MimeType
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data), true
}

func resolveUploadedFileRefsValue(v any) (any, bool) {
	switch x := v.(type) {
	case map[string]any:
		changed := false
		if fileID, ok := x["file_id"].(string); ok && strings.HasPrefix(fileID, openAIFilePrefix) {
			if dataURL, exists := resolveOpenAIFileDataURL(fileID); exists {
				x["file_data"] = dataURL
				if t, _ := x["type"].(string); t == "file" || t == "input_file" {
					if _, hasFile := x["file"]; !hasFile {
						file := map[string]any{
							"file_id":   fileID,
							"file_data": dataURL,
						}
						if filename, ok := x["filename"].(string); ok && filename != "" {
							file["filename"] = filename
						}
						x["file"] = file
					}
				}
				changed = true
			}
		}
		if fileObj, ok := x["file"].(map[string]any); ok {
			if next, ok := resolveUploadedFileRefsValue(fileObj); ok {
				x["file"] = next
				changed = true
			}
		}
		for key, val := range x {
			next, ok := resolveUploadedFileRefsValue(val)
			if ok {
				x[key] = next
				changed = true
			}
		}
		return x, changed
	case []any:
		changed := false
		for i, val := range x {
			next, ok := resolveUploadedFileRefsValue(val)
			if ok {
				x[i] = next
				changed = true
			}
		}
		return x, changed
	default:
		return v, false
	}
}

func ResolveUploadedFileReferences(c *gin.Context) {
	contentType := c.Request.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "application/json") {
		return
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return
	}
	body, err := storage.Bytes()
	if err != nil || !strings.Contains(string(body), openAIFilePrefix) {
		return
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return
	}
	next, changed := resolveUploadedFileRefsValue(payload)
	if !changed {
		return
	}
	encoded, err := json.Marshal(next)
	if err != nil {
		return
	}
	newStorage, err := common.CreateBodyStorage(encoded)
	if err != nil {
		return
	}
	c.Set(common.KeyBodyStorage, newStorage)
	c.Request.Body = io.NopCloser(newStorage)
	c.Request.ContentLength = int64(len(encoded))
}
