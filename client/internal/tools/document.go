package tools

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// read_document caps. A document is meant to be read in one model turn, not streamed.
const (
	DefaultMaxFileBytes = 25 << 20 // 25 MiB on-disk file cap
	DefaultMaxTextBytes = 2 << 20  // 2 MiB extracted-text cap
)

// Reader extracts text from a local file, confined to BaseDir. It is the implementation
// behind the read_document curated tool. The agent never gets a raw filesystem Read tool —
// it may only name a file inside the sandbox's input directory, which the runner populates
// (an attachment, or something fetch_url already downloaded).
type Reader struct {
	BaseDir      string // the only directory read_document may read from (no traversal out)
	MaxFileBytes int64
	MaxTextBytes int
}

// DocResult is the structured tool output.
type DocResult struct {
	Path      string `json:"path"`      // path as requested
	Kind      string `json:"kind"`      // text|html|pdf
	Text      string `json:"text"`      // extracted text (truncated to MaxTextBytes)
	Truncated bool   `json:"truncated"` // true if the text hit the cap
	Bytes     int    `json:"bytes"`     // number of text bytes returned
}

func NewReader(baseDir string) *Reader {
	return &Reader{BaseDir: baseDir, MaxFileBytes: DefaultMaxFileBytes, MaxTextBytes: DefaultMaxTextBytes}
}

// resolve confines a requested path to BaseDir, defeating ../ traversal and absolute-path
// escapes. It also resolves symlinks on the parent so a symlink inside BaseDir cannot point
// the read outside it.
func (r *Reader) resolve(reqPath string) (string, error) {
	if r.BaseDir == "" {
		return "", fmt.Errorf("read_document: no base directory configured")
	}
	base, err := filepath.Abs(r.BaseDir)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(base); err == nil {
		base = resolved
	}
	// Treat the request as relative to base; an absolute request is re-rooted under base.
	clean := filepath.Clean("/" + strings.TrimSpace(reqPath)) // leading-slash trick drops ../ escapes
	full := filepath.Join(base, clean)
	// Resolve symlinks on the final path if it exists, then re-check containment.
	if resolved, err := filepath.EvalSymlinks(full); err == nil {
		full = resolved
	}
	if full != base && !strings.HasPrefix(full, base+string(os.PathSeparator)) {
		return "", fmt.Errorf("read_document: path %q escapes the allowed directory", reqPath)
	}
	return full, nil
}

// ReadDocument reads + extracts text from a file inside BaseDir.
func (r *Reader) ReadDocument(reqPath string) (*DocResult, error) {
	full, err := r.resolve(reqPath)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return nil, fmt.Errorf("read_document: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("read_document: %q is a directory", reqPath)
	}
	maxFile := r.MaxFileBytes
	if maxFile <= 0 {
		maxFile = DefaultMaxFileBytes
	}
	if info.Size() > maxFile {
		return nil, fmt.Errorf("read_document: file is %d bytes, over the %d-byte cap", info.Size(), maxFile)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("read_document: %w", err)
	}

	kind, text := extractText(full, data)
	if kind == "" {
		return nil, fmt.Errorf("read_document: unsupported or binary file %q", reqPath)
	}

	maxText := r.MaxTextBytes
	if maxText <= 0 {
		maxText = DefaultMaxTextBytes
	}
	truncated := false
	if len(text) > maxText {
		text = text[:maxText]
		truncated = true
	}
	return &DocResult{Path: reqPath, Kind: kind, Text: text, Truncated: truncated, Bytes: len(text)}, nil
}

// extractText dispatches by extension, then content sniff. Returns kind="" if it can't
// extract readable text (binary it doesn't understand) so the caller fails closed.
func extractText(path string, data []byte) (kind, text string) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".txt", ".md", ".markdown", ".csv", ".tsv", ".log", ".json", ".yaml", ".yml", ".xml", ".rst":
		return "text", string(data)
	case ".html", ".htm":
		return "html", stripHTML(data)
	case ".pdf":
		return "pdf", extractPDFText(data)
	}
	// No/unknown extension: sniff.
	if bytes.HasPrefix(data, []byte("%PDF-")) {
		return "pdf", extractPDFText(data)
	}
	lower := bytes.ToLower(bytes.TrimSpace(data))
	if bytes.HasPrefix(lower, []byte("<!doctype html")) || bytes.HasPrefix(lower, []byte("<html")) {
		return "html", stripHTML(data)
	}
	if looksTextual(data) {
		return "text", string(data)
	}
	return "", ""
}

// looksTextual reports whether data is valid UTF-8 dominated by printable characters — a
// conservative gate so read_document refuses to dump raw binary as "text".
func looksTextual(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	sample := data
	if len(sample) > 8192 {
		sample = sample[:8192]
	}
	if !utf8.Valid(sample) {
		return false
	}
	printable := 0
	for _, b := range sample {
		if b == '\t' || b == '\n' || b == '\r' || b >= 0x20 {
			printable++
		}
	}
	return float64(printable)/float64(len(sample)) > 0.85
}

// stripHTML removes script/style blocks and tags, returning readable text. It is a
// best-effort textualiser, not a sanitiser — the output is data handed back to the model.
func stripHTML(data []byte) string {
	s := string(data)
	s = removeBlock(s, "<script", "</script>")
	s = removeBlock(s, "<style", "</style>")
	var b strings.Builder
	inTag := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '<':
			inTag = true
		case '>':
			inTag = false
			b.WriteByte(' ')
		default:
			if !inTag {
				b.WriteByte(s[i])
			}
		}
	}
	return collapseSpaces(unescapeEntities(b.String()))
}

func removeBlock(s, open, close string) string {
	for {
		lower := strings.ToLower(s)
		i := strings.Index(lower, open)
		if i < 0 {
			return s
		}
		j := strings.Index(lower[i:], close)
		if j < 0 {
			return s[:i] // unterminated: drop the rest
		}
		s = s[:i] + " " + s[i+j+len(close):]
	}
}

func unescapeEntities(s string) string {
	r := strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", "\"", "&#39;", "'", "&apos;", "'", "&nbsp;", " ",
	)
	return r.Replace(s)
}

func collapseSpaces(s string) string {
	var b strings.Builder
	var lastSpace, lastNL bool
	for _, r := range s {
		switch r {
		case ' ', '\t':
			if !lastSpace && !lastNL {
				b.WriteByte(' ')
			}
			lastSpace = true
		case '\n', '\r':
			if !lastNL {
				b.WriteByte('\n')
			}
			lastNL = true
			lastSpace = false
		default:
			b.WriteRune(r)
			lastSpace, lastNL = false, false
		}
	}
	return strings.TrimSpace(b.String())
}
