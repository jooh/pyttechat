package markdown

import "strings"

type BlockStreamer struct {
	full    strings.Builder
	pending strings.Builder
	flushed bool
}

func (s *BlockStreamer) Add(delta string) []string {
	if delta == "" {
		return nil
	}
	s.flushed = false
	s.full.WriteString(delta)
	s.pending.WriteString(delta)
	return s.completedBlocks(false)
}

func (s *BlockStreamer) Flush() []string {
	return s.completedBlocks(true)
}

func (s *BlockStreamer) FullMarkdown() string {
	return s.full.String()
}

func (s *BlockStreamer) completedBlocks(final bool) []string {
	if s.flushed && final {
		return nil
	}
	text := s.pending.String()
	var blocks []string
	var blockStart int
	var lineStart int
	inFence := false
	var fenceMarker byte
	var fenceLength int

	for lineStart < len(text) {
		lineEnd := strings.IndexByte(text[lineStart:], '\n')
		if lineEnd < 0 {
			break
		}
		lineEnd += lineStart + 1
		line := text[lineStart:lineEnd]
		trimmed := strings.TrimSpace(line)

		if inFence {
			if isClosingFence(trimmed, fenceMarker, fenceLength) {
				inFence = false
				fenceMarker = 0
				fenceLength = 0
				if lineEnd < len(text) && isBlankLineAt(text, lineEnd) {
					nextLineEnd := lineEnd + blankLineLength(text[lineEnd:])
					blocks = append(blocks, text[blockStart:nextLineEnd])
					blockStart = nextLineEnd
					lineStart = nextLineEnd
					continue
				}
				if lineEnd == len(text) {
					blocks = append(blocks, text[blockStart:lineEnd])
					blockStart = lineEnd
				}
				lineStart = lineEnd
				continue
			}
			lineStart = lineEnd
			continue
		}

		if marker, length, ok := openingFence(trimmed); ok {
			inFence = true
			fenceMarker = marker
			fenceLength = length
			lineStart = lineEnd
			continue
		}

		if !inFence && trimmed == "" {
			blocks = append(blocks, text[blockStart:lineEnd])
			blockStart = lineEnd
		}
		lineStart = lineEnd
	}

	if final && blockStart < len(text) {
		blocks = append(blocks, text[blockStart:])
		blockStart = len(text)
	}
	if blockStart > 0 {
		remaining := text[blockStart:]
		s.pending.Reset()
		s.pending.WriteString(remaining)
	}
	if final && s.pending.Len() == 0 {
		s.flushed = true
	}
	return blocks
}

func openingFence(trimmed string) (byte, int, bool) {
	if len(trimmed) < 3 {
		return 0, 0, false
	}
	marker := trimmed[0]
	if marker != '`' && marker != '~' {
		return 0, 0, false
	}
	length := markerRunLength(trimmed, marker)
	if length < 3 {
		return 0, 0, false
	}
	return marker, length, true
}

func isClosingFence(trimmed string, marker byte, minLength int) bool {
	if len(trimmed) < minLength {
		return false
	}
	length := markerRunLength(trimmed, marker)
	if length < minLength {
		return false
	}
	return strings.TrimSpace(trimmed[length:]) == ""
}

func markerRunLength(text string, marker byte) int {
	var length int
	for length < len(text) && text[length] == marker {
		length++
	}
	return length
}

func isBlankLineAt(text string, index int) bool {
	return text[index] == '\n' || text[index] == '\r'
}

func blankLineLength(text string) int {
	if strings.HasPrefix(text, "\r\n") {
		return 2
	}
	return 1
}
