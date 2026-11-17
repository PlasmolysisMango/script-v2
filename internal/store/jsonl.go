package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"jbs/internal/engine"
)

// JSONL implements engine.Store and engine.SnapshotStore. Pinned room directories
// and no-follow opens confine private persistence to <root>/<roomID>/.
type JSONL struct {
	mu   sync.Mutex
	root *os.Root
	err  error
}

var _ engine.Store = (*JSONL)(nil)
var _ engine.SnapshotStore = (*JSONL)(nil)

func NewRoomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate room id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// ValidRoomID accepts only the canonical format produced by NewRoomID and
// engine.NewToken, never user-supplied names or path components.
func ValidRoomID(id string) bool {
	if len(id) != 32 || id != strings.ToLower(id) {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func New(dir string) (*JSONL, error) {
	if dir == "" {
		return nil, fmt.Errorf("store directory is required")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("store directory: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create store directory: %w", err)
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return nil, fmt.Errorf("stat store directory: %w", err)
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("store directory must be a real directory")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open store directory: %w", err)
	}
	if err := root.Chmod(".", 0o700); err != nil {
		return nil, errors.Join(fmt.Errorf("secure store directory: %w", err), root.Close())
	}
	return &JSONL{root: root}, nil
}

func (s *JSONL) Append(roomID string, e engine.Event) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() { s.recordError("append", err) }()
	if !ValidRoomID(roomID) {
		return fmt.Errorf("unsafe room id %q", roomID)
	}
	if s.root == nil {
		return fmt.Errorf("event store is closed")
	}
	b, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encode event: %w", err)
	}
	b = append(b, '\n')
	room, err := s.roomDir(roomID)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, room.Close()) }()
	f, err := openRegular(room, "events.jsonl", os.O_WRONLY|os.O_CREATE|os.O_APPEND)
	if err != nil {
		return fmt.Errorf("open event log: %w", err)
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	if n, err := f.Write(b); err != nil {
		return fmt.Errorf("append event: %w", err)
	} else if n != len(b) {
		return fmt.Errorf("short event write: %d of %d bytes", n, len(b))
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync event log: %w", err)
	}
	return nil
}

// roomDir creates and pins a real room directory. Checking its identity after
// OpenRoot rejects a replacement between the no-follow stat and the open.
// All subsequent operations, including rename, use the pinned directory.
func (s *JSONL) roomDir(roomID string) (room *os.Root, err error) {
	if err := s.root.Mkdir(roomID, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("create room directory: %w", err)
	}
	fi, err := s.root.Lstat(roomID)
	if err != nil {
		return nil, fmt.Errorf("stat room directory: %w", err)
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("room directory must be a real directory")
	}
	room, err = s.root.OpenRoot(roomID)
	if err != nil {
		return nil, fmt.Errorf("open room directory: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, room.Close())
			room = nil
		}
	}()
	pinned, err := room.Stat(".")
	if err != nil {
		return room, fmt.Errorf("stat pinned room directory: %w", err)
	}
	if !os.SameFile(fi, pinned) {
		return room, fmt.Errorf("room directory changed while opening")
	}
	if err := room.Chmod(".", 0o700); err != nil {
		return room, fmt.Errorf("secure room directory: %w", err)
	}
	return room, nil
}

func checkRegular(fi os.FileInfo) error {
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("must be a regular file")
	}
	if stat, ok := fi.Sys().(*syscall.Stat_t); ok && stat.Nlink != 1 {
		return fmt.Errorf("must not be hard-linked")
	}
	return nil
}

func openRegular(root *os.Root, name string, flags int) (f *os.File, err error) {
	f, err = root.OpenFile(name, flags|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, f.Close())
			if flags&os.O_CREATE != 0 && flags&os.O_EXCL != 0 {
				err = errors.Join(err, root.Remove(name))
			}
			f = nil
		}
	}()
	fi, err := f.Stat()
	if err != nil {
		return f, err
	}
	if err := checkRegular(fi); err != nil {
		return f, err
	}
	if err := f.Chmod(0o600); err != nil {
		return f, err
	}
	return f, nil
}

// Snapshot atomically replaces private inspection data in the pinned roomdir.
// A failed encode or write leaves the previous snapshot intact.
func (s *JSONL) Snapshot(roomID string, value any) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() { s.recordError("snapshot", err) }()
	if !ValidRoomID(roomID) {
		return fmt.Errorf("unsafe room id %q", roomID)
	}
	if s.root == nil {
		return fmt.Errorf("event store is closed")
	}
	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode snapshot: %w", err)
	}
	b = append(b, '\n')
	room, err := s.roomDir(roomID)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, room.Close()) }()
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return fmt.Errorf("generate snapshot tempfile: %w", err)
	}
	temp := ".snapshot-" + hex.EncodeToString(random[:]) + ".tmp"
	f, err := openRegular(room, temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return fmt.Errorf("create snapshot tempfile: %w", err)
	}
	renamed := false
	defer func() {
		if f != nil {
			err = errors.Join(err, f.Close())
		}
		if !renamed {
			err = errors.Join(err, room.Remove(temp))
		}
	}()
	if n, err := f.Write(b); err != nil {
		return fmt.Errorf("write snapshot: %w", err)
	} else if n != len(b) {
		return fmt.Errorf("short snapshot write: %d of %d bytes", n, len(b))
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync snapshot: %w", err)
	}
	closeErr := f.Close()
	f = nil
	if closeErr != nil {
		return fmt.Errorf("close snapshot tempfile: %w", closeErr)
	}
	// Rename does not follow its destination, but reject unsafe existing files
	// rather than silently replacing symlinks, special files, or hard links.
	fi, err := room.Lstat("snapshot.json")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat snapshot: %w", err)
	}
	if err == nil {
		if err := checkRegular(fi); err != nil {
			return fmt.Errorf("snapshot: %w", err)
		}
	}
	if err := room.Rename(temp, "snapshot.json"); err != nil {
		return fmt.Errorf("replace snapshot: %w", err)
	}
	renamed = true
	dir, err := room.Open(".")
	if err != nil {
		return fmt.Errorf("open snapshot directory for sync: %w", err)
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync snapshot directory: %w", err)
	}
	return nil
}

// recordError is called with s.mu held, after operation cleanup completes.
func (s *JSONL) recordError(operation string, err error) {
	if err != nil {
		s.err = errors.Join(s.err, err)
		log.Printf("event store %s failed: %v", operation, err)
	}
}

// Err exposes accumulated append and snapshot failures.
func (s *JSONL) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *JSONL) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root == nil {
		return nil
	}
	err := s.root.Close()
	s.root = nil
	return err
}
