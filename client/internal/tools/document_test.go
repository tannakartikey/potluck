package tools

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadDocumentText(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "note.md", []byte("# Title\n\nSome **markdown** body."))
	r := NewReader(dir)
	res, err := r.ReadDocument("note.md")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != "text" || !strings.Contains(res.Text, "**markdown** body") {
		t.Errorf("got kind=%q text=%q", res.Kind, res.Text)
	}
}

func TestReadDocumentHTML(t *testing.T) {
	dir := t.TempDir()
	html := []byte(`<!doctype html><html><head><style>.x{color:red}</style>` +
		`<script>alert(1)</script></head><body><h1>Heading</h1><p>Para&nbsp;text &amp; more.</p></body></html>`)
	writeFile(t, dir, "page.html", html)
	r := NewReader(dir)
	res, err := r.ReadDocument("page.html")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != "html" {
		t.Fatalf("kind = %q", res.Kind)
	}
	if !strings.Contains(res.Text, "Heading") || !strings.Contains(res.Text, "Para text & more") {
		t.Errorf("stripped text = %q", res.Text)
	}
	if strings.Contains(res.Text, "alert(1)") || strings.Contains(res.Text, "color:red") {
		t.Errorf("script/style content leaked into text: %q", res.Text)
	}
}

// uncompressedPDF builds a tiny valid-enough PDF with an unfiltered content stream.
func uncompressedPDF(content string) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n1 0 obj\n<< /Length ")
	b.WriteString(fmt.Sprintf("%d", len(content)))
	b.WriteString(" >>\nstream\n")
	b.WriteString(content)
	b.WriteString("\nendstream\nendobj\n%%EOF\n")
	return b.Bytes()
}

// flatePDF builds a tiny PDF whose content stream is FlateDecode-compressed.
func flatePDF(t *testing.T, content string) []byte {
	t.Helper()
	var z bytes.Buffer
	w := zlib.NewWriter(&z)
	if _, err := w.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n2 0 obj\n<< /Filter /FlateDecode /Length ")
	b.WriteString(fmt.Sprintf("%d", z.Len()))
	b.WriteString(" >>\nstream\n")
	b.Write(z.Bytes())
	b.WriteString("\nendstream\nendobj\n%%EOF\n")
	return b.Bytes()
}

func TestReadDocumentPDFUncompressed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.pdf", uncompressedPDF("BT /F1 24 Tf 72 700 Td (Hello World) Tj ET"))
	r := NewReader(dir)
	res, err := r.ReadDocument("doc.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != "pdf" {
		t.Fatalf("kind = %q", res.Kind)
	}
	if !strings.Contains(res.Text, "Hello World") {
		t.Errorf("PDF text = %q, want it to contain 'Hello World'", res.Text)
	}
}

func TestReadDocumentPDFFlate(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.pdf", flatePDF(t, "BT (Compressed PDF text) Tj ET"))
	r := NewReader(dir)
	res, err := r.ReadDocument("doc.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "Compressed PDF text") {
		t.Errorf("flate PDF text = %q", res.Text)
	}
}

func TestReadDocumentPDFOperators(t *testing.T) {
	dir := t.TempDir()
	// TJ array with a large negative kern (→ space), a hex string, and an octal escape.
	content := `BT [(Hel)-278(lo)] TJ <20576F726C64> Tj (paren\051end\0501\051) Tj ET`
	writeFile(t, dir, "ops.pdf", uncompressedPDF(content))
	r := NewReader(dir)
	res, err := r.ReadDocument("ops.pdf")
	if err != nil {
		t.Fatal(err)
	}
	// [(Hel)-278(lo)] → "Hel lo"; <20576F726C64> → " World"; \051=')' \050='(' \051=')'
	if !strings.Contains(res.Text, "Hel lo") {
		t.Errorf("TJ kerning not handled: %q", res.Text)
	}
	if !strings.Contains(res.Text, "World") {
		t.Errorf("hex string not decoded: %q", res.Text)
	}
	if !strings.Contains(res.Text, "paren)end(1)") {
		t.Errorf("octal/paren escapes not handled: %q", res.Text)
	}
}

func TestReadDocumentRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	// Put a secret OUTSIDE the base dir.
	parent := filepath.Dir(dir)
	secret := filepath.Join(parent, "outside-secret.txt")
	if err := os.WriteFile(secret, []byte("TOPSECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(secret)

	r := NewReader(dir)
	for _, p := range []string{"../outside-secret.txt", "../../etc/passwd", "/etc/passwd", "subdir/../../outside-secret.txt"} {
		res, err := r.ReadDocument(p)
		if err == nil {
			t.Errorf("ReadDocument(%q) should be refused, got text=%q", p, res.Text)
		}
	}
}

func TestReadDocumentRejectsBinary(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "blob.bin", []byte{0x00, 0x01, 0x02, 0xff, 0xfe, 0x00, 0x10, 0x80})
	r := NewReader(dir)
	if _, err := r.ReadDocument("blob.bin"); err == nil {
		t.Error("a binary file with no known extension should be refused")
	}
}

func TestReadDocumentSizeCap(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "big.txt", bytes.Repeat([]byte("x"), 1000))
	r := NewReader(dir)
	r.MaxFileBytes = 100
	if _, err := r.ReadDocument("big.txt"); err == nil {
		t.Error("file over MaxFileBytes should be refused")
	}
}

func TestReadDocumentTextCap(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "big.txt", bytes.Repeat([]byte("y"), 1000))
	r := NewReader(dir)
	r.MaxTextBytes = 50
	res, err := r.ReadDocument("big.txt")
	if err != nil {
		t.Fatal(err)
	}
	if res.Bytes != 50 || !res.Truncated {
		t.Errorf("text cap not applied: bytes=%d truncated=%v", res.Bytes, res.Truncated)
	}
}
