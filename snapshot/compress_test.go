package snapshot

import (
	"bytes"
	"compress/gzip"
	"io"
	"testing"

	"github.com/klauspost/compress/zstd"
)

var sample = bytes.Repeat([]byte("nodes,edges,files,project_metadata\n"), 200)

func TestCompressGzipRoundTrip(t *testing.T) {
	out, err := CompressGzip(sample)
	if err != nil {
		t.Fatal(err)
	}
	r, err := gzip.NewReader(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, sample) {
		t.Error("gzip round-trip mismatch")
	}
	if len(out) >= len(sample) {
		t.Errorf("gzip did not compress: %d >= %d", len(out), len(sample))
	}
}

func TestCompressZstdRoundTrip(t *testing.T) {
	out, err := CompressZstd(sample)
	if err != nil {
		t.Fatal(err)
	}
	d, err := zstd.NewReader(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got, err := io.ReadAll(d)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, sample) {
		t.Error("zstd round-trip mismatch")
	}
	if len(out) >= len(sample) {
		t.Errorf("zstd did not compress: %d >= %d", len(out), len(sample))
	}
}

func TestCompressDeterministic(t *testing.T) {
	for _, fn := range []struct {
		name string
		f    func([]byte) ([]byte, error)
	}{
		{"gzip", CompressGzip},
		{"zstd", CompressZstd},
	} {
		t.Run(fn.name, func(t *testing.T) {
			a, err := fn.f(sample)
			if err != nil {
				t.Fatal(err)
			}
			b, err := fn.f(sample)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(a, b) {
				t.Errorf("%s output not deterministic", fn.name)
			}
		})
	}
}
