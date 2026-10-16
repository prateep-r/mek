// Command catalog writes mek's resource catalogs (internal/catalog/*.json.gz)
// from the clouds' own models: the AWS CLI's command index and botocore
// service models, and gcloud's command tree. Run it with `make catalog`;
// the files it writes are committed, so building mek needs neither CLI.
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/prateep-r/mek/internal/catalog"
)

func main() {
	awscli := flag.String("awscli", "", "the AWS CLI's awscli package directory (with data/ac.index and botocore/data)")
	clouds := flag.String("clouds", "aws,gcp", "which catalogs to write")
	out := flag.String("out", "internal/catalog", "where to write the catalogs")
	flag.Parse()
	for _, cloud := range strings.Split(*clouds, ",") {
		if err := run(cloud, *awscli, *out); err != nil {
			fmt.Fprintln(os.Stderr, "catalog:", err)
			os.Exit(1)
		}
	}
}

func run(cloud, awscli, out string) error {
	var c catalog.Catalog
	switch cloud {
	case "aws":
		if awscli == "" {
			var err error
			if awscli, err = findAWSCLI(); err != nil {
				return err
			}
		}
		m, err := loadAWS(awscli)
		if err != nil {
			return err
		}
		c = m.catalog(awsVersion())
	case "gcp":
		t, version, err := loadGCP()
		if err != nil {
			return err
		}
		c = t.catalog(version)
	default:
		return fmt.Errorf("unknown cloud %q", cloud)
	}
	tune(&c)
	return write(filepath.Join(out, cloud+".json.gz"), c)
}

// findAWSCLI locates the awscli package next to the aws on PATH.
func findAWSCLI() (string, error) {
	bin, err := exec.LookPath("aws")
	if err != nil {
		return "", err
	}
	if out, err := exec.Command("asdf", "which", "aws").Output(); err == nil && strings.Contains(bin, "/.asdf/shims/") {
		bin = strings.TrimSpace(string(out))
	}
	bin, _ = filepath.EvalSymlinks(bin)
	for dir := filepath.Dir(bin); dir != "/"; dir = filepath.Dir(dir) {
		matches, _ := filepath.Glob(filepath.Join(dir, "**", "awscli", "data", "ac.index"))
		more, _ := filepath.Glob(filepath.Join(dir, "awscli", "data", "ac.index"))
		deep, _ := filepath.Glob(filepath.Join(dir, "lib", "*", "site-packages", "awscli", "data", "ac.index"))
		for _, m := range append(append(more, matches...), deep...) {
			return filepath.Dir(filepath.Dir(m)), nil
		}
	}
	return "", fmt.Errorf("no awscli package found near %s — pass -awscli", bin)
}

func awsVersion() string {
	out, _ := exec.Command("aws", "--version").Output()
	v, _, _ := strings.Cut(strings.TrimPrefix(strings.TrimSpace(string(out)), "aws-cli/"), " ")
	return v
}

// write saves a catalog as gzipped JSON, the same bytes for the same input.
func write(path string, c catalog.Catalog) error {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	enc := json.NewEncoder(zw)
	if err := enc.Encode(c); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
