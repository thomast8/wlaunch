package cache

import (
	"os"
	"testing"
	"time"
)

func TestReadWriteRoundTrip(t *testing.T) {
	t.Setenv("WLAUNCH_CACHE_DIR", t.TempDir())
	store := Default()
	Write(store, KeyPRs("/repo"), []string{"one", "two"})

	got, saved, ok := Read[[]string](store, KeyPRs("/repo"))
	if !ok {
		t.Fatal("expected cache hit")
	}
	if saved.IsZero() {
		t.Fatal("expected saved timestamp")
	}
	if len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("cache value = %#v", got)
	}
}

func TestCorruptCacheMisses(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WLAUNCH_CACHE_DIR", dir)
	store := Default()
	Write(store, KeyRepos(), []string{"ok"})
	for _, entry := range mustEntries(t, dir) {
		if err := os.WriteFile(dir+"/"+entry.Name(), []byte("not json"), 0o600); err != nil {
			t.Fatalf("corrupt cache: %v", err)
		}
	}
	if _, _, ok := Read[[]string](store, KeyRepos()); ok {
		t.Fatal("corrupt cache should miss")
	}
}

func TestFresh(t *testing.T) {
	if !Fresh(time.Now(), time.Minute) {
		t.Fatal("current timestamp should be fresh")
	}
	if Fresh(time.Now().Add(-2*time.Minute), time.Minute) {
		t.Fatal("old timestamp should be stale")
	}
}

func mustEntries(t *testing.T, dir string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	return entries
}
