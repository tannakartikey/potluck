package tools

import (
	"bytes"
	"compress/zlib"
	"io"
	"strconv"
	"strings"
)

// extractPDFText is a best-effort, pure-Go PDF text extractor. It is deliberately modest and
// honestly bounded: it handles text-based PDFs whose content streams are either uncompressed
// or FlateDecode-compressed (the overwhelmingly common case) with standard string encodings.
// It does NOT handle encrypted PDFs, image-only/scanned PDFs (no OCR), or exotic CID-font
// ToUnicode CMaps — those yield partial or empty text, never a crash. See read_document docs.
func extractPDFText(data []byte) string {
	var out strings.Builder
	for _, stream := range pdfContentStreams(data) {
		appendContentStreamText(&out, stream)
	}
	return collapseSpaces(out.String())
}

// pdfContentStreams returns the decoded bytes of each stream that plausibly holds page
// content. Image/font/other-filter streams are skipped (we only inflate FlateDecode and
// pass through unfiltered streams).
func pdfContentStreams(data []byte) [][]byte {
	var streams [][]byte
	const kw = "stream"
	search := data
	offset := 0
	for {
		i := bytes.Index(search, []byte(kw))
		if i < 0 {
			break
		}
		abs := offset + i
		// The dictionary preceding this stream tells us the filter; look back a bounded window.
		dictStart := abs - 512
		if dictStart < 0 {
			dictStart = 0
		}
		dict := strings.ToLower(string(data[dictStart:abs]))

		// Advance past "stream" + its trailing EOL (CRLF or LF, per the PDF spec).
		dataStart := abs + len(kw)
		if dataStart < len(data) && data[dataStart] == '\r' {
			dataStart++
		}
		if dataStart < len(data) && data[dataStart] == '\n' {
			dataStart++
		}
		end := bytes.Index(data[dataStart:], []byte("endstream"))
		if end < 0 {
			break
		}
		raw := data[dataStart : dataStart+end]
		// Trim a single trailing EOL before endstream.
		raw = bytes.TrimRight(raw, "\r\n")

		offset = dataStart + end + len("endstream")
		search = data[offset:]

		// Skip streams that are clearly not page text (images / other binary filters).
		if strings.Contains(dict, "dctdecode") || strings.Contains(dict, "jpxdecode") ||
			strings.Contains(dict, "ccittfaxdecode") || strings.Contains(dict, "jbig2decode") ||
			strings.Contains(dict, "/image") {
			continue
		}
		if strings.Contains(dict, "flatedecode") {
			if dec, err := flateInflate(raw); err == nil {
				streams = append(streams, dec)
			}
			continue
		}
		// No (recognised) filter: treat as a raw content stream.
		streams = append(streams, raw)
	}
	return streams
}

func flateInflate(b []byte) ([]byte, error) {
	zr, err := zlib.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	// Cap inflation to avoid a zip-bomb expanding unbounded.
	return io.ReadAll(io.LimitReader(zr, 64<<20))
}

// appendContentStreamText walks a content stream and emits text only on the PDF text-showing
// operators (Tj, ', ", TJ), with newlines on the text-positioning operators (Td, TD, T*).
// Operator-gated extraction avoids dumping non-text operands as garbage.
func appendContentStreamText(out *strings.Builder, cs []byte) {
	var pendingStr string // last literal/hex string operand seen
	var pendingArr string // text gathered from the last [ ... ] array (for TJ)
	haveStr, haveArr := false, false

	i, n := 0, len(cs)
	for i < n {
		c := cs[i]
		switch {
		case c == '(':
			s, ni := readPDFLiteralString(cs, i)
			pendingStr, haveStr = s, true
			i = ni
		case c == '<' && i+1 < n && cs[i+1] != '<':
			s, ni := readPDFHexString(cs, i)
			pendingStr, haveStr = s, true
			i = ni
		case c == '[':
			s, ni := readPDFArrayText(cs, i)
			pendingArr, haveArr = s, true
			i = ni
		case isOpChar(c):
			op, ni := readOp(cs, i)
			i = ni
			switch op {
			case "Tj", "'", "\"":
				if haveStr {
					out.WriteString(pendingStr)
					out.WriteByte(' ')
					haveStr = false
				}
			case "TJ":
				if haveArr {
					out.WriteString(pendingArr)
					out.WriteByte(' ')
					haveArr = false
				}
			case "Td", "TD", "T*", "BT", "ET":
				out.WriteByte('\n')
			}
		default:
			i++
		}
	}
}

func isOpChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '\'' || c == '"' || c == '*'
}

func readOp(cs []byte, i int) (string, int) {
	start := i
	for i < len(cs) && isOpChar(cs[i]) {
		i++
	}
	return string(cs[start:i]), i
}

// readPDFLiteralString reads a (...) string starting at the '(' index, handling balanced
// nested parens, backslash escapes, and octal \ddd codes. Returns the decoded string and the
// index just past the closing ')'.
func readPDFLiteralString(cs []byte, i int) (string, int) {
	var b strings.Builder
	i++ // consume the opening '(' (not part of the string, not a depth level)
	depth := 0
	for i < len(cs) {
		c := cs[i]
		if c == '\\' && i+1 < len(cs) {
			nx := cs[i+1]
			switch nx {
			case 'n':
				b.WriteByte('\n')
				i += 2
			case 'r':
				b.WriteByte('\r')
				i += 2
			case 't':
				b.WriteByte('\t')
				i += 2
			case 'b':
				b.WriteByte('\b')
				i += 2
			case 'f':
				b.WriteByte('\f')
				i += 2
			case '(', ')', '\\':
				b.WriteByte(nx)
				i += 2
			case '\r', '\n': // line continuation: backslash-newline → nothing
				i += 2
				if nx == '\r' && i < len(cs) && cs[i] == '\n' {
					i++
				}
			default:
				if nx >= '0' && nx <= '7' { // octal escape, up to 3 digits
					j, val := i+1, 0
					for j < len(cs) && j < i+4 && cs[j] >= '0' && cs[j] <= '7' {
						val = val*8 + int(cs[j]-'0')
						j++
					}
					b.WriteByte(byte(val))
					i = j
				} else {
					b.WriteByte(nx)
					i += 2
				}
			}
			continue
		}
		switch c {
		case '(':
			depth++
			b.WriteByte(c)
			i++
		case ')':
			if depth == 0 {
				return b.String(), i + 1
			}
			depth--
			b.WriteByte(c)
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String(), i
}

// readPDFHexString reads a <...> hex string and decodes it to bytes.
func readPDFHexString(cs []byte, i int) (string, int) {
	i++ // skip '<'
	var hex strings.Builder
	for i < len(cs) && cs[i] != '>' {
		c := cs[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			hex.WriteByte(c)
		}
		i++
	}
	if i < len(cs) {
		i++ // skip '>'
	}
	h := hex.String()
	if len(h)%2 == 1 {
		h += "0"
	}
	var b strings.Builder
	for k := 0; k+1 < len(h); k += 2 {
		if v, err := strconv.ParseUint(h[k:k+2], 16, 8); err == nil {
			b.WriteByte(byte(v))
		}
	}
	return b.String(), i
}

// readPDFArrayText reads a [ ... ] TJ array, concatenating its string elements and inserting
// a space where a large negative number (inter-word spacing adjustment) appears.
func readPDFArrayText(cs []byte, i int) (string, int) {
	i++ // skip '['
	var b strings.Builder
	for i < len(cs) && cs[i] != ']' {
		c := cs[i]
		switch {
		case c == '(':
			s, ni := readPDFLiteralString(cs, i)
			b.WriteString(s)
			i = ni
		case c == '<':
			s, ni := readPDFHexString(cs, i)
			b.WriteString(s)
			i = ni
		case c == '-' || (c >= '0' && c <= '9'):
			start := i
			for i < len(cs) && (cs[i] == '-' || cs[i] == '.' || (cs[i] >= '0' && cs[i] <= '9')) {
				i++
			}
			if v, err := strconv.ParseFloat(string(cs[start:i]), 64); err == nil && v < -100 {
				b.WriteByte(' ')
			}
		default:
			i++
		}
	}
	if i < len(cs) {
		i++ // skip ']'
	}
	return b.String(), i
}
