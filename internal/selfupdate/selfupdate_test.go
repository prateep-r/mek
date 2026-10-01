package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func makeTGZ(t *testing.T, name string, body []byte) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "README.md", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("hi"))
	_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(body)
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestVerifyAndExtract(t *testing.T) {
	tgz := makeTGZ(t, "mek", []byte("BINARY"))
	sum := sha256.Sum256(tgz)
	sums := []byte("deadbeef  other.tar.gz\n" + hex.EncodeToString(sum[:]) + "  mek_darwin_arm64.tar.gz\n")

	if err := verify(tgz, sums, "mek_darwin_arm64.tar.gz"); err != nil {
		t.Fatal(err)
	}
	if err := verify(append(tgz, 0), sums, "mek_darwin_arm64.tar.gz"); err == nil {
		t.Fatal("tampered archive must fail verification")
	}
	if err := verify(tgz, sums, "mek_linux_amd64.tar.gz"); err == nil {
		t.Fatal("missing asset must fail")
	}
	bin, err := extract(tgz, "mek")
	if err != nil || string(bin) != "BINARY" {
		t.Fatalf("extract: %q %v", bin, err)
	}
}

func TestIsHomebrew(t *testing.T) {
	if !IsHomebrew("/opt/homebrew/Caskroom/mek/0.1.0/mek") || IsHomebrew("/Users/me/.local/bin/mek") {
		t.Fatal("IsHomebrew wrong")
	}
}
