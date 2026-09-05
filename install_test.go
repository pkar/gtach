package gtach

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallerChecksums(t *testing.T) {
	payload := []byte("#!/bin/sh\necho 0.1.0\n")
	digest := fmt.Sprintf("%x", sha256.Sum256(payload))
	for name, checksums := range map[string]string{
		"valid":     digest + "  gtach-linux-arm64\n",
		"corrupt":   strings.Repeat("0", 64) + "  gtach-linux-arm64\n",
		"missing":   digest + "  gtach-linux-amd64\n",
		"duplicate": digest + "  gtach-linux-arm64\n" + digest + "  gtach-linux-arm64\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			dest := filepath.Join(dir, "install")
			os.Mkdir(bin, 0700)
			os.WriteFile(filepath.Join(dir, "payload"), payload, 0600)
			os.WriteFile(filepath.Join(dir, "checksums"), []byte(checksums), 0600)
			os.WriteFile(filepath.Join(bin, "uname"), []byte("#!/bin/sh\ncase \"$1\" in -s) echo Linux;; -m) echo aarch64;; esac\n"), 0700)
			os.WriteFile(filepath.Join(bin, "curl"), []byte(`#!/bin/sh
out=""
while [ "$#" -gt 0 ]; do
 case "$1" in -o) out=$2; shift;; https://*) url=$1;; esac
 shift
done
case "$url" in
 https://github.com/pkar/gtach/releases/latest) printf 'https://github.com/pkar/gtach/releases/tag/v0.1.0';;
 https://github.com/pkar/gtach/releases/download/v0.1.0/gtach-linux-arm64) cp "$FIXTURE/payload" "$out";;
 https://github.com/pkar/gtach/releases/download/v0.1.0/checksums.txt) cp "$FIXTURE/checksums" "$out";;
 *) exit 1;;
esac
`), 0700)
			cmd := exec.Command("sh", "install.sh")
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "FIXTURE="+dir, "GTACH_INSTALL_DIR="+dest)
			b, err := cmd.CombinedOutput()
			if name == "valid" {
				if err != nil {
					t.Fatalf("install: %v %s", err, b)
				}
				got, err := os.ReadFile(filepath.Join(dest, "gtach"))
				if err != nil || string(got) != string(payload) {
					t.Fatalf("installed: %q %v", got, err)
				}
			} else {
				if err == nil {
					t.Fatal("accepted invalid checksums")
				}
				if _, err := os.Stat(filepath.Join(dest, "gtach")); !os.IsNotExist(err) {
					t.Fatal("installed unverified binary")
				}
			}
		})
	}
}
