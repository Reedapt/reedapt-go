package reedapt

import (
	"bytes"
	"io"
	"sort"
)

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

func sortParts(parts []UploadedPart) {
	sort.Slice(parts, func(i, j int) bool { return parts[i].PartNumber < parts[j].PartNumber })
}
