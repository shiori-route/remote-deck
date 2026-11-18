package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const TokenFile = "auth.token"

// LoadOrCreate requires the caller to own and lock a private data directory.
// Invalid existing files fail closed; they are never overwritten or regenerated.
func LoadOrCreate(directory string) (Verifier, error) {
	path := filepath.Join(directory, TokenFile)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if errors.Is(err, os.ErrExist) {
		return load(path)
	}
	if err != nil {
		return Verifier{}, err
	}
	var entropy [32]byte
	_, _ = rand.Read(entropy[:])
	token := hex.EncodeToString(entropy[:])
	_, writeErr := io.WriteString(f, token+"\n")
	err = errors.Join(writeErr, f.Sync(), f.Close())
	if err != nil {
		return Verifier{}, err
	}
	dir, err := os.Open(directory)
	if err != nil {
		return Verifier{}, err
	}
	if err = errors.Join(dir.Sync(), dir.Close()); err != nil {
		return Verifier{}, err
	}
	return New(token)
}

func load(path string) (Verifier, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return Verifier{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Verifier{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		return Verifier{}, errors.New("access token file must be private, regular, owned and not hard-linked")
	}
	// Valid files contain 64 bytes, optionally followed by a single LF.
	data, err := io.ReadAll(io.LimitReader(f, 66))
	if err != nil {
		return Verifier{}, err
	}
	return New(strings.TrimSuffix(string(data), "\n"))
}
