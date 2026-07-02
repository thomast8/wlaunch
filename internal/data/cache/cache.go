package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const CloudTTL = time.Minute

type Store struct {
	dir string
}

type envelope struct {
	SavedAt time.Time       `json:"savedAt"`
	Data    json.RawMessage `json:"data"`
}

func Default() *Store {
	if dir := os.Getenv("WLAUNCH_CACHE_DIR"); dir != "" {
		return &Store{dir: dir}
	}
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = filepath.Join(os.TempDir(), "wlaunch-cache")
	}
	return &Store{dir: filepath.Join(base, "wlaunch")}
}

func KeyRepos() string { return "repos:v1" }

func KeyPRs(repo string) string { return "prs:v1:" + repo }

func KeyBranches(repo string) string { return "branches:v1:" + repo }

func KeyWorktrees(repo string) string { return "worktrees:v1:" + repo }

func KeyActionableRepo(repo string) string { return "actionable:repo:v1:" + repo }

func KeyActionableAllRepos() string { return "actionable:all:v1" }

func KeySlugToPath() string { return "slug-to-path:v1" }

func Read[T any](s *Store, key string) (T, time.Time, bool) {
	var zero T
	if s == nil {
		return zero, time.Time{}, false
	}
	b, err := os.ReadFile(s.path(key))
	if err != nil {
		return zero, time.Time{}, false
	}
	var env envelope
	if err := json.Unmarshal(b, &env); err != nil || env.SavedAt.IsZero() {
		return zero, time.Time{}, false
	}
	var out T
	if err := json.Unmarshal(env.Data, &out); err != nil {
		return zero, time.Time{}, false
	}
	return out, env.SavedAt, true
}

func Write[T any](s *Store, key string, value T) {
	if s == nil {
		return
	}
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	env, err := json.Marshal(envelope{SavedAt: time.Now(), Data: data})
	if err != nil {
		return
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return
	}
	path := s.path(key)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, env, 0o600); err != nil {
		return
	}
	_ = os.Chmod(tmp, 0o600)
	_ = os.Rename(tmp, path)
}

func Fresh(saved time.Time, ttl time.Duration) bool {
	return !saved.IsZero() && time.Since(saved) <= ttl
}

func (s *Store) path(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(s.dir, hex.EncodeToString(sum[:])+".json")
}
