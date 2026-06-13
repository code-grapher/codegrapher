package snapshot

import (
	"bytes"
	"compress/gzip"

	"github.com/klauspost/compress/zstd"
)

// zstdEncoder is shared and concurrency-safe; created once at package init.
var zstdEncoder, _ = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))

// CompressGzip returns the deterministic gzip encoding of data. The gzip header
// carries no name and a zero mtime, so re-compressing identical input yields
// identical bytes.
func CompressGzip(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// CompressZstd returns the zstd encoding of data at the default level. zstd is
// deterministic for a fixed input and level.
func CompressZstd(data []byte) ([]byte, error) {
	return zstdEncoder.EncodeAll(data, nil), nil
}
