package auth

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestTokenPersistenceAndRotation(t *testing.T) {
	dir := t.TempDir()
	v, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, TokenFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 65 || data[64] != '\n' {
		t.Fatal("wrong generated token format")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("permissions: %v %v", info, err)
	}
	header := []string{"Bearer " + strings.TrimSuffix(string(data), "\n")}
	reloaded, err := LoadOrCreate(dir)
	if err != nil || !v.Allows(header) || !reloaded.Allows(header) {
		t.Fatal("token did not survive reload")
	}
	other, err := LoadOrCreate(t.TempDir())
	if err != nil || other.Allows(header) {
		t.Fatal("independent instance shared credentials")
	}
	// Replacing the private file changes credentials only on the next load.
	replacement := strings.Repeat("c1", 32)
	if err := os.WriteFile(path, []byte(replacement+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rotated, err := LoadOrCreate(dir)
	if err != nil || rotated.Allows(header) || !rotated.Allows([]string{"Bearer " + replacement}) {
		t.Fatal("rotation failed")
	}
	if !v.Allows(header) {
		t.Fatal("in-memory verifier changed unexpectedly")
	}
}

func TestInvalidTokenFilesFailClosed(t *testing.T) {
	for _, contents := range []string{"", "partial", strings.Repeat("g", 64), strings.Repeat("a", 64) + "\n\n", strings.Repeat("a", 1<<16)} {
		dir := t.TempDir()
		path := filepath.Join(dir, TokenFile)
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadOrCreate(dir); err == nil {
			t.Fatal("invalid token file accepted")
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != contents {
			t.Fatal("invalid existing token was replaced")
		}
	}
	for _, kind := range []string{"public", "symlink", "hardlink", "directory", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, TokenFile)
			target := filepath.Join(t.TempDir(), "secret")
			original := strings.Repeat("ab", 32)
			if err := os.WriteFile(target, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "public":
				err = os.WriteFile(path, []byte(original), 0644)
				if err == nil {
					err = os.Chmod(path, 0644)
				}
			case "symlink":
				err = os.Symlink(target, path)
			case "hardlink":
				err = os.Link(target, path)
			case "directory":
				err = os.Mkdir(path, 0700)
			case "fifo":
				err = syscall.Mkfifo(path, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := LoadOrCreate(dir); err == nil {
				t.Fatal("unsafe token file accepted")
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != original {
				t.Fatal("modified target")
			}
		})
	}
}
