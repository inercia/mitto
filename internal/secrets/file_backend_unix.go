//go:build linux || darwin

package secrets

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"

	"golang.org/x/sys/unix"
)

var vaultTempCounter uint64

// FileBackend stores one opaque vault document in a hardened local file.
type FileBackend struct {
	path         string
	pathResolver func() (string, error)
}

// NewFileBackend creates a backend at an explicit path, primarily for tests.
func NewFileBackend(path string) *FileBackend { return &FileBackend{path: path} }

func (b *FileBackend) IsSupported() bool { return b != nil }

// Load reads the vault without following the credential directory or file symlinks.
func (b *FileBackend) Load() ([]byte, error) {
	path, err := b.resolvePath()
	if err != nil {
		return nil, err
	}
	dirFD, err := openSecureCredentialDir(filepath.Dir(path), false)
	if err != nil {
		return nil, err
	}
	defer unix.Close(dirFD)

	name := filepath.Base(path)
	fd, err := unix.Openat(dirFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("%w: open vault file", ErrUnsafeVaultPath)
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		unix.Close(fd)
		return nil, errors.New("open credential vault file")
	}
	defer file.Close()
	if err := validateFileDescriptor(fd); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("read credential vault: %w", err)
	}
	return data, nil
}

// Save fsyncs a mode-0600 temporary file, atomically renames it, then fsyncs
// the mode-0700 parent directory. Existing unsafe targets are rejected.
func (b *FileBackend) Save(data []byte) error {
	path, err := b.resolvePath()
	if err != nil {
		return err
	}
	dirFD, err := openSecureCredentialDir(filepath.Dir(path), true)
	if err != nil {
		return err
	}
	defer unix.Close(dirFD)

	name := filepath.Base(path)
	if err := validateExistingFileAt(dirFD, name); err != nil {
		return err
	}
	tmpName := fmt.Sprintf(".vault-%d-%d.tmp", os.Getpid(), atomic.AddUint64(&vaultTempCounter, 1))
	tmpFD, err := unix.Openat(dirFD, tmpName,
		unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return fmt.Errorf("create credential vault temporary file: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = unix.Unlinkat(dirFD, tmpName, 0)
		}
	}()

	if err := unix.Fchmod(tmpFD, 0600); err != nil {
		unix.Close(tmpFD)
		return fmt.Errorf("set credential vault permissions: %w", err)
	}
	tmpFile := os.NewFile(uintptr(tmpFD), tmpName)
	if tmpFile == nil {
		unix.Close(tmpFD)
		return errors.New("open credential vault temporary file")
	}
	if _, err := io.Copy(tmpFile, bytes.NewReader(data)); err != nil {
		tmpFile.Close()
		return fmt.Errorf("write credential vault: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		return fmt.Errorf("sync credential vault: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close credential vault: %w", err)
	}
	if err := unix.Renameat(dirFD, tmpName, dirFD, name); err != nil {
		return fmt.Errorf("replace credential vault: %w", err)
	}
	cleanup = false
	if err := unix.Fsync(dirFD); err != nil {
		return fmt.Errorf("sync credential vault directory: %w", err)
	}
	return nil
}

func (b *FileBackend) resolvePath() (string, error) {
	if b == nil {
		return "", ErrNotSupported
	}
	path := b.path
	if b.pathResolver != nil {
		var err error
		path, err = b.pathResolver()
		if err != nil {
			return "", fmt.Errorf("resolve credential vault path: %w", err)
		}
	}
	if path == "" || filepath.Base(path) == "." || filepath.Base(path) == string(filepath.Separator) {
		return "", fmt.Errorf("%w: empty vault path", ErrUnsafeVaultPath)
	}
	return filepath.Clean(path), nil
}

func openSecureCredentialDir(dir string, create bool) (int, error) {
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) && create {
		parent := filepath.Dir(dir)
		parentInfo, parentErr := os.Lstat(parent)
		if errors.Is(parentErr, os.ErrNotExist) {
			// Create only MITTO_DIR itself. Never recursively change the expected
			// permissions of ancestors such as XDG_DATA_HOME.
			if err := os.Mkdir(parent, 0700); err != nil {
				return -1, fmt.Errorf("create credential vault parent: %w", err)
			}
			parentInfo, parentErr = os.Lstat(parent)
		}
		if parentErr != nil {
			return -1, fmt.Errorf("inspect credential vault parent: %w", parentErr)
		}
		if parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() {
			return -1, fmt.Errorf("%w: credential parent is not a directory", ErrUnsafeVaultPath)
		}
		if err := os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return -1, fmt.Errorf("create credential vault directory: %w", err)
		}
		info, err = os.Lstat(dir)
	}
	if errors.Is(err, os.ErrNotExist) {
		return -1, ErrNotFound
	}
	if err != nil {
		return -1, fmt.Errorf("inspect credential vault directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != 0700 {
		return -1, fmt.Errorf("%w: credential directory must be a mode-0700 directory", ErrUnsafeVaultPath)
	}
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, fmt.Errorf("%w: open credential directory", ErrUnsafeVaultPath)
	}
	if err := validateDirectoryDescriptor(fd); err != nil {
		unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

func validateDirectoryDescriptor(fd int) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return fmt.Errorf("inspect credential directory: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0777 != 0700 || stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("%w: credential directory mode or owner", ErrUnsafeVaultPath)
	}
	return nil
}

func validateExistingFileAt(dirFD int, name string) error {
	var stat unix.Stat_t
	err := unix.Fstatat(dirFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect credential vault: %w", err)
	}
	return validateFileStat(&stat)
}

func validateFileDescriptor(fd int) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return fmt.Errorf("inspect credential vault: %w", err)
	}
	return validateFileStat(&stat)
}

func validateFileStat(stat *unix.Stat_t) error {
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0777 != 0600 || stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("%w: vault file must be owner-owned, regular, and mode 0600", ErrUnsafeVaultPath)
	}
	return nil
}
